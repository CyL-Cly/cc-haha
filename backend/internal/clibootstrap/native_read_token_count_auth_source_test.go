package clibootstrap

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestActualSourceMCPReadCountUsesHelperAndSubscriberCredentials(t *testing.T) {
	if os.Getenv("CC_HAHA_SOURCE_BUN") == "" {
		t.Skip("actual Source Bun required")
	}
	for _, profile := range []string{"helper", "subscriber"} {
		t.Run(profile, func(t *testing.T) {
			home, env := ordinaryFixture(t)
			delete(env, "ANTHROPIC_API_KEY")
			env["ANTHROPIC_MODEL"] = "fixture-model"
			if profile == "helper" {
				command := "printf owned-helper-token"
				if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), rawMain(map[string]any{"apiKeyHelper": command}), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				env["CC_HAHA_MCP_FAKE_SECURE_JSON"] = string(rawMain(map[string]any{"claudeAiOauth": map[string]any{"accessToken": "fixture-subscriber", "refreshToken": "fixture-refresh", "expiresAt": time.Now().Add(time.Hour).UnixMilli(), "scopes": []string{"user:inference", "user:profile"}, "subscriptionType": "max", "rateLimitTier": "default_claude_max_5x"}}))
			}
			type observed struct {
				Key, Token, Beta string
				HasKey           bool
				Body             json.RawMessage
			}
			var mu sync.Mutex
			rows := []observed{}
			peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				rows = append(rows, observed{r.Header.Get("x-api-key"), r.Header.Get("authorization"), r.Header.Get("anthropic-beta"), len(r.Header.Values("x-api-key")) > 0, append(json.RawMessage(nil), body...)})
				mu.Unlock()
				if r.URL.Path != "/v1/messages/count_tokens" {
					t.Error("Read sampled messages")
				}
				w.Header().Set("content-type", "application/json")
				io.WriteString(w, `{"input_tokens":37}`)
			}))
			defer peer.Close()
			cert := filepath.Join(home, "count-cert.pem")
			if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			env["ANTHROPIC_BASE_URL"], env["NODE_EXTRA_CA_CERTS"] = peer.URL, cert
			path := filepath.Join(home, "actual-long-auth-history.txt")
			if err := os.WriteFile(path, []byte(strings.Repeat("owned readonly history", 1400)), 0600); err != nil {
				t.Fatal(err)
			}
			source := startOrdinarySource(t, home, home, env)
			want := mcpOrdinaryResult(t, source.call(t, "Read", map[string]any{"file_path": path}))
			source.close(t)
			host, _ := ordinaryGoContext(t, home, home, env)
			data, err := host.Call(context.Background(), "Read", rawMain(map[string]any{"file_path": path}))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(mcpOrdinaryResult(t, data), want) {
				t.Fatal("credential Read result differs from actual Source")
			}
			mu.Lock()
			defer mu.Unlock()
			if len(rows) != 2 || !reflect.DeepEqual(rows[0], rows[1]) {
				t.Fatalf("Source count credential/body/beta mismatch: requests=%d SourceBeta=%s GoBeta=%s bodyEqual=%v", len(rows), rows[0].Beta, rows[len(rows)-1].Beta, reflect.DeepEqual(rows[0].Body, rows[len(rows)-1].Body))
			}
			if profile == "helper" {
				if rows[0].Key != "owned-helper-token" || rows[0].Token != "Bearer owned-helper-token" {
					t.Error("helper not actually executed/consumed")
				}
			} else if rows[0].Token != "Bearer fixture-subscriber" || !strings.Contains(rows[0].Beta, "oauth-2025-04-20") {
				t.Error("subscriber not actually consumed")
			}
		})
	}
}
