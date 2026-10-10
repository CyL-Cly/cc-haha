package desktopoauth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestOpenAILegacyStoreAndRefreshMatchesActualBundledService(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	now := int64(1770000000000)
	rows := []map[string]any{{}, {"tokens": nil}, {"tokens": false}, {"tokens": true}, {"tokens": "old-primitive"}, {"tokens": []any{1}}}
	for _, expiry := range []any{nil, now + 300001, now + 300000, now - 1, "bad"} {
		for _, optional := range []any{nil, "", false, 0, "legacy-value"} {
			rows = append(rows, map[string]any{"tokens": map[string]any{"accessToken": "fake-old", "refreshToken": "fake-refresh", "expiresAt": expiry, "idToken": optional, "email": optional, "accountId": optional, "clientId": optional, "future": map[string]any{"keep": true}}, "response": map[string]any{"access_token": "fake-new", "expires_in": 3600}})
		}
	}
	rows = append(rows, map[string]any{"tokens": map[string]any{"accessToken": "fake-old", "expiresAt": now - 1}}, map[string]any{"tokens": map[string]any{"accessToken": "fake-old", "refreshToken": "fake-refresh", "expiresAt": now - 1}, "refreshError": "private fixture refresh failed"})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-openai-store.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_OPENAI_STORE_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"now": now, "rows": rows})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source store %v %s", err, stderr.String())
	}
	var source []json.RawMessage
	if json.Unmarshal(output, &source) != nil || len(source) != len(rows) {
		t.Fatalf("Source store profiles %s", output)
	}
	for i, row := range rows {
		calls := []string{}
		host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			calls = append(calls, r.Form.Get("refresh_token"))
			if row["refreshError"] != nil {
				w.WriteHeader(401)
				_, _ = io.WriteString(w, "private fixture refresh failed")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(encode(row["response"]))
		}))
		private := t.TempDir()
		parent, err := New(Options{Home: private, ConfigDir: filepath.Join(private, ".claude"), Platform: "linux", Environment: func() map[string]string { return map[string]string{} }, Now: func() time.Time { return time.UnixMilli(now) }, HTTPClient: &http.Client{Transport: openAITestTransport{host.URL, http.DefaultTransport}}})
		if err != nil {
			t.Fatal(err)
		}
		service := parent.OpenAI()
		if token, present := row["tokens"]; present {
			if err = os.MkdirAll(filepath.Dir(service.TokenPath()), 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(service.TokenPath(), encode(token), 0600); err != nil {
				t.Fatal(err)
			}
		}
		doc, err := service.EnsureFreshTokens(ctx)
		if err != nil {
			t.Fatal(err)
		}
		result := map[string]any{"value": nil, "calls": calls, "stored": nil, "mode": nil}
		if doc != nil {
			result["value"] = doc.Data
		}
		if data, e := os.ReadFile(service.TokenPath()); e == nil {
			result["stored"] = string(data)
			info, e := os.Stat(service.TokenPath())
			if e != nil {
				t.Fatal(e)
			}
			result["mode"] = uint32(info.Mode().Perm())
		}
		parent.Close()
		host.Close()
		var got, want any
		_ = json.Unmarshal(encode(result), &got)
		_ = json.Unmarshal(source[i], &want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("row%d actual store%s want%s", i, encode(result), source[i])
		}
	}
}
