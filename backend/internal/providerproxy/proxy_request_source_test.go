package providerproxy

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
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestProxyRequestAgainstActualHandlerWireProduction(t *testing.T) {
	rows := []map[string]any{}
	for _, format := range []string{"anthropic", "openai_chat", "openai_responses"} {
		for _, path := range []string{"", "/v1/", "/deepseek", "/opencode.ai/zen/go/v1"} {
			for _, auth := range []string{"api_key", "auth_token", "auth_token_empty_api_key", "dual_same_token", "dual_dummy"} {
				config := map[string]any{"id": "fixture", "name": "Fixture", "apiKey": "fixture-key", "apiFormat": format, "supportsNestedToolResultMedia": false, "authStrategy": auth, "modelApiFormats": []any{}, "upstreamHeaders": map[string]string{"User-Agent": "fixture/$VERSION", "x-gateway-session": "$SESSION_ID", "Authorization": "must-not-win", "host": "must-not-win", "x-extra": " value "}}
				rows = append(rows, map[string]any{"config": config, "basePath": path, "scoped": true, "body": map[string]any{"model": "deepseek-v4-flash[1M]", "stream": false, "max_tokens": 32000, "thinking": map[string]string{"type": "disabled"}, "system": "system", "messages": []any{map[string]any{"role": "user", "content": "fixture"}}, "metadata": map[string]string{"user_id": "account_session_stable"}}, "headers": map[string]string{"x-claude-code-session-id": " header-id ", "x-cc-haha-output-budget-source": "default", "anthropic-version": "2023-06-01", "anthropic-beta": "fixture-beta", "Authorization": "Bearer must-not-pass", "x-api-key": "must-not-pass", "Connection": "keep-alive, x-private-hop", "x-private-hop": "must-not-pass", "x-app": "cli", "x-client-app": "must-not-pass", "x-caller": "kept", "User-Agent": "caller"}})
			}
		}
	}
	for _, nested := range []bool{false, true} {
		for _, model := range []string{"kimi-k3", "deepseek-chat", "vendor/deepseek-reasoner", "claude-test", "generic-vision", "space-bunny-free"} {
			rows = append(rows, map[string]any{"config": map[string]any{"id": "fixture", "name": "Fixture", "apiKey": "", "apiFormat": "openai_chat", "supportsNestedToolResultMedia": nested, "authStrategy": "api_key", "modelApiFormats": []any{map[string]any{"prefixes": []string{"claude"}, "apiFormat": "anthropic"}}}, "basePath": "/opencode.ai/zen/go/v1", "body": map[string]any{"model": model, "max_tokens": 1024, "messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "image", "source": map[string]string{"type": "url", "url": "https://fixture.invalid/image"}}, map[string]string{"type": "text", "text": "fixture"}}}}}, "headers": map[string]string{}})
		}
	}
	rows = append(rows, map[string]any{"config": map[string]any{"id": "fixture", "name": "Fixture", "apiKey": "", "apiFormat": "anthropic", "supportsNestedToolResultMedia": true, "authStrategy": "api_key"}, "basePath": "", "body": map[string]any{"model": "claude", "messages": []any{}}})
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-providerproxy-handler-request.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "NODE_ENV=test", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(requestEncode(rows))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Handler Source %v %s", err, stderr.String())
	}
	var expected []struct {
		Captured *struct {
			Path    string
			Headers map[string]string
			Body    json.RawMessage
		}
		Status   int
		Response json.RawMessage
	}
	if json.Unmarshal(output, &expected) != nil || len(expected) != len(rows) {
		t.Fatal(string(output), stderr.String())
	}
	for i, row := range rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			c := responseObject(requestEncode(row["config"]))
			config := ProxyConfig{ID: c.String("id"), Name: c.String("name"), APIKey: c.String("apiKey"), APIFormat: c.String("apiFormat"), AuthStrategy: c.String("authStrategy"), SupportsNestedToolResultMedia: string(c.Get("supportsNestedToolResultMedia")) == "true", ModelAPIFormats: c.Get("modelApiFormats"), UpstreamHeaders: c.Get("upstreamHeaders"), RequestCompatibility: c.Get("requestCompatibility"), BaseURL: "http://127.0.0.1" + row["basePath"].(string)}
			incoming := http.Header{}
			if headers, ok := row["headers"].(map[string]string); ok {
				for k, v := range headers {
					incoming.Set(k, v)
				}
			}
			scoped, _ := row["scoped"].(bool)
			plan, err := PrepareProxyRequest(config, responseObject(requestEncode(row["body"])), incoming, "proxy-fixture", scoped)
			if expected[i].Captured == nil {
				if err == nil {
					t.Fatal("Source did not forward but Go did", expected[i].Status, string(expected[i].Response))
				}
				if failure, ok := err.(*ResponseError); ok && failure.Status != expected[i].Status {
					t.Fatal(err, expected[i].Status)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if plan.URL != "http://127.0.0.1"+expected[i].Captured.Path {
				t.Fatal(plan.URL, expected[i].Captured.Path)
			}
			if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(expected[i].Captured.Body), requestEncode(plan.Upstream)) {
				t.Fatalf("Source%s Go%s", expected[i].Captured.Body, requestEncode(plan.Upstream))
			}
			// The Source fixture observes fetch's physical wire, so compare the
			// native HTTP wire too. A raw producer header (e.g. "Bearer ") has
			// not yet crossed fetch/HTTP's framing boundary.
			var wire http.Header
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wire = r.Header.Clone()
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer upstream.Close()
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream.URL+expected[i].Captured.Path, bytes.NewReader(requestEncode(plan.Upstream)))
			if err != nil {
				t.Fatal(err)
			}
			request.Header = plan.Headers.Clone()
			response, err := upstream.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			for name := range plan.Headers {
				if value := expected[i].Captured.Headers[strings.ToLower(name)]; value != wire.Get(name) {
					t.Errorf("header%s Source%q Go%q", name, value, wire.Get(name))
				}
			}
		})
	}
}
