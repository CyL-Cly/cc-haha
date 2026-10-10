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
	"strings"
	"testing"
	"time"
)

func TestOpenAITokenTransportMatchesActualBundledSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	rows := []map[string]any{}
	for _, operation := range []string{"exchange", "refresh"} {
		for _, response := range []struct {
			status int
			body   string
		}{{200, `{"access_token":"fake","refresh_token":"fake-refresh","expires_in":12}`}, {200, "\ufeff{\"access_token\":\"fake-bom\",\"refresh_token\":\"fake-refresh\"}"}, {401, `{"ACCESS_TOKEN":"private-fixture-secret","other":"preserved","code_verifier":"private-verifier"}`}, {403, `access_token=private-access&id_token=private-id&other=stay`}, {500, strings.Repeat("A", 900)}} {
			rows = append(rows, map[string]any{"operation": operation, "status": response.status, "body": response.body, "input": map[string]string{"code": "fixture +/界", "redirectUri": "http://localhost:1455/auth/callback", "codeVerifier": "fixture-verifier ~*"}, "refreshToken": "fixture-refresh +/界"})
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-openai-client.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_OPENAI_CLIENT_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"now": 1770000000000, "rows": []any{}, "transportRows": rows})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source transport %v %s", e, stderr.String())
	}
	var source struct{ Requests, TransportResults []json.RawMessage }
	if json.Unmarshal(output, &source) != nil || len(source.Requests) != len(rows) {
		t.Fatalf("Source profiles %s", output)
	}
	for i, row := range rows {
		var actual any
		host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			data, _ := io.ReadAll(r.Body)
			headers := map[string]any{}
			for _, name := range []string{"accept", "content-type", "user-agent", "authorization"} {
				if value := r.Header.Get(name); value != "" {
					headers[name] = value
				} else {
					headers[name] = nil
				}
			}
			actual = map[string]any{"method": r.Method, "path": r.URL.Path, "body": string(data), "headers": headers}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(row["status"].(int))
			_, _ = io.WriteString(w, row["body"].(string))
		}))
		parent, err := New(Options{Home: home, ConfigDir: filepath.Join(home, ".claude"), Platform: "linux", Environment: func() map[string]string { return map[string]string{} }, HTTPClient: &http.Client{Transport: openAITestTransport{host.URL, http.DefaultTransport}}})
		if err != nil {
			t.Fatal(err)
		}
		var value any
		result := map[string]any{}
		if row["operation"] == "exchange" {
			input := row["input"].(map[string]string)
			v, e := parent.exchangeOpenAICode(ctx, input["code"], input["redirectUri"], input["codeVerifier"])
			value = v
			err = e
		} else {
			v, e := parent.refreshOpenAITokens(ctx, row["refreshToken"].(string))
			value = v
			err = e
		}
		if err != nil {
			result["error"] = err.Error()
		} else {
			result["value"] = value
		}
		parent.Close()
		host.Close()
		var expected, got any
		_ = json.Unmarshal(source.Requests[i], &expected)
		_ = json.Unmarshal(encode(actual), &got)
		if !reflect.DeepEqual(got, expected) {
			t.Errorf("row%d actual wire%s want%s", i, encode(actual), source.Requests[i])
		}
		_ = json.Unmarshal(source.TransportResults[i], &expected)
		_ = json.Unmarshal(encode(result), &got)
		if !reflect.DeepEqual(got, expected) {
			t.Errorf("row%d actual result%s want%s", i, encode(result), source.TransportResults[i])
		}
	}
}
