package grokauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestGrokFreshForceCacheAndAtomicFilesAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	const now int64 = 1770000000000
	response := map[string]any{"status": 200, "body": `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600,"scope":"new-scope"}`}
	rows := []map[string]any{}
	for _, expires := range []int64{now + 600001, now + 300001, now + 300000, now - 1} {
		for _, force := range [][]bool{{false, false}, {true, false, true}} {
			rows = append(rows, map[string]any{"initial": map[string]any{"accessToken": "initial-access", "refreshToken": "initial-refresh", "expiresAt": expires, "clientId": "private-client", "email": "before@invalid", "future": true}, "force": force, "responses": []any{response}})
		}
	}
	rows = append(rows, map[string]any{"force": []bool{false, true}, "responses": []any{response}}, map[string]any{"initial": "not-json", "force": []bool{false, true}, "responses": []any{response}})
	for _, body := range []string{`{"access_token":"new-access","expires_in":3600}`, `{"access_token":"new-access","expires_in":0}`, `{"access_token":"secret","refresh_token":"also-secret"}`} {
		status := 200
		if len(rows) == 12 {
			status = 401
		}
		rows = append(rows, map[string]any{"initial": map[string]any{"accessToken": "old", "refreshToken": "old-refresh", "expiresAt": now - 1}, "force": []bool{false, false}, "responses": []any{map[string]any{"status": status, "body": body}}})
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-grok-refresh.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_GROK_REFRESH_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(rows)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	source, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var expected []json.RawMessage
	if err = json.Unmarshal(source, &expected); err != nil || len(expected) != len(rows) {
		t.Fatal(err, string(source))
	}
	for i, row := range rows {
		path := filepath.Join(home, fmt.Sprintf("go-grok-%d.json", i))
		if initial, present := row["initial"]; present {
			raw, ok := initial.(string)
			if !ok {
				data, _ := json.Marshal(initial)
				raw = string(data)
			}
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
		}
		requests := []any{}
		peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.ReadAll(r.Body)
			responses := row["responses"].([]any)
			response := responses[0].(map[string]any)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(response["status"].(int))
			_, _ = io.WriteString(w, response["body"].(string))
		}))
		service, err := New(Options{Environment: func() map[string]string { return map[string]string{"GROK_OAUTH_FILE": path} }, NowMillis: func() int64 { return now }, Read: func(_ context.Context, path string) ([]byte, error) { return os.ReadFile(path) }, Save: func(_ context.Context, path string, tokens protocol.JSONObject) (bool, error) {
			temporary := path + ".tmp.private"
			data, _ := json.MarshalIndent(tokens, "", "  ")
			data = append(data, '\n')
			if err := os.WriteFile(temporary, data, 0600); err != nil {
				return false, nil
			}
			if err := os.Rename(temporary, path); err != nil {
				_ = os.Remove(temporary)
				return false, nil
			}
			return true, nil
		}, Refresh: func(ctx context.Context, token, client json.RawMessage) (protocol.JSONObject, error) {
			plan, err := desktopoauth.GrokRefreshRequest(token, client)
			if err != nil {
				return protocol.JSONObject{}, err
			}
			headers := [][2]string{}
			for _, header := range plan.Headers {
				headers = append(headers, [2]string{http.CanonicalHeaderKey(header[0]), header[1]})
			}
			sort.Slice(headers, func(i, j int) bool { return headers[i][0] < headers[j][0] })
			lower := [][2]string{}
			for _, h := range headers {
				key := h[0]
				for index, letter := range []byte(key) {
					if letter >= 'A' && letter <= 'Z' {
						key = key[:index] + string(letter+32) + key[index+1:]
					}
				}
				lower = append(lower, [2]string{key, h[1]})
			}
			requests = append(requests, map[string]any{"url": plan.URL, "method": plan.Method, "body": plan.Body, "headers": lower})
			request, _ := http.NewRequestWithContext(ctx, plan.Method, peer.URL, bytes.NewBufferString(plan.Body))
			for _, h := range plan.Headers {
				request.Header.Set(h[0], h[1])
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				return protocol.JSONObject{}, err
			}
			defer response.Body.Close()
			data, readErr := io.ReadAll(response.Body)
			return desktopoauth.DecodeGrokRefreshResponse(response.StatusCode, data, readErr)
		}})
		if err != nil {
			t.Fatal(err)
		}
		calls := []any{}
		for _, force := range row["force"].([]bool) {
			value, err := service.Fresh(context.Background(), force)
			if err != nil {
				calls = append(calls, map[string]any{"error": err.Error()})
			} else {
				calls = append(calls, map[string]any{"value": value})
			}
		}
		peer.Close()
		var file, mode any
		if data, err := os.ReadFile(path); err == nil {
			file = string(data)
			info, _ := os.Stat(path)
			mode = info.Mode().Perm()
		}
		actual, _ := json.Marshal(map[string]any{"calls": calls, "requests": requests, "file": file, "mode": mode})
		var got, want any
		_ = json.Unmarshal(actual, &got)
		_ = json.Unmarshal(expected[i], &want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("case%d Go%s Source%s", i, actual, expected[i])
		}
	}
}
