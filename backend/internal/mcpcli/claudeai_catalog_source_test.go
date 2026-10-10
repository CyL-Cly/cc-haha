package mcpcli

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
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestClaudeAIOrganizationCatalogPhysicalSourceRequestsAndCollisions(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	rows := []string{
		`{"data":[]}`,
		`{"data":[{"display_name":"Example Server!","url":"https://owned.invalid/one","id":"a"},{"display_name":"Example Server?","url":"https://owned.invalid/two","id":"b"},{"display_name":"Example Server 2","url":"https://owned.invalid/three","id":"c"},{"display_name":"Example Server!","url":"https://owned.invalid/four","id":"d"},{"display_name":"__中文 🐟__","url":"https://owned.invalid/five","id":"e"}]}`,
		`{"data":[{"display_name":null,"id":0},{"display_name":true,"url":null},{"display_name":["a",null,"b"],"id":[1,2]},{"id":"missing-name"},7,false,"primitive"]}`,
		`{"data":null}`, `{"data":{}}`, `{"data":[{"display_name":"first"},null]}`, `not-json`,
	}
	for index, body := range rows {
		t.Run(body[:min(len(body), 50)], func(t *testing.T) {
			var mu sync.Mutex
			requests := []http.Header{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.RequestURI() != "/v1/mcp_servers?limit=1000" {
					t.Errorf("unexpected actual request %s %s", r.Method, r.URL)
				}
				if b, e := io.ReadAll(r.Body); e != nil || len(b) != 0 {
					t.Errorf("GET body %q %v", b, e)
				}
				mu.Lock()
				requests = append(requests, r.Header.Clone())
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				if index == len(rows)-1 {
					w.WriteHeader(401)
				}
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			home := t.TempDir()
			tokens := json.RawMessage(`{"accessToken":"fake-catalog-token","scopes":["user:mcp_servers"]}`)
			data, _ := json.Marshal(map[string]any{"homeDir": home, "address": server.URL, "tokens": tokens})
			fixture, _ := filepath.Abs("claudeai_catalog.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLAUDEAI_CATALOG_FIXTURE=1", "PATH=/usr/bin:/bin", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file"}
			cmd.Stdin = bytes.NewReader(data)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, e := cmd.Output()
			if e != nil {
				t.Fatal("actual Source", e, stderr.String())
			}
			secure := protocol.NewJSONObject(protocol.JSONField{Name: "claudeAiOauth", Value: tokens})
			encoded, _ := secure.MarshalJSON()
			catalog := ClaudeAICatalog{HTTP: server.Client(), BaseURL: server.URL, Environment: map[string]string{}, Tokens: func(context.Context) (json.RawMessage, error) { return encoded, nil }}
			got := catalog.Configurations(ctx)
			actual, _ := got.MarshalJSON()
			var expected, decoded any
			if json.Unmarshal(out, &expected) != nil || json.Unmarshal(actual, &decoded) != nil || !reflect.DeepEqual(expected, decoded) {
				t.Fatalf("Source %s Go %s", out, actual)
			}
			_ = catalog.Configurations(ctx)
			mu.Lock()
			defer mu.Unlock()
			if len(requests) != 2 {
				t.Fatalf("Source/Go actual request + memo hit expected2, got%d", len(requests))
			}
			for _, key := range []string{"Authorization", "Content-Type", "Anthropic-Beta", "Anthropic-Version", "User-Agent", "Accept", "Accept-Encoding"} {
				if requests[0].Get(key) != requests[1].Get(key) {
					t.Errorf("actual header %s Source%q Go%q", key, requests[0].Get(key), requests[1].Get(key))
				}
			}
		})
	}
}
