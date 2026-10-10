package clibootstrap

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/networksettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualCLIStandaloneRandomPortRunsWriteReadAndClosesListener(t *testing.T) {
	var requests atomic.Int32
	var target string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Error("wrong upstream protocol path", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer private-proxy-fixture" {
			t.Error("saved provider auth not consumed")
		}
		body, _ := io.ReadAll(r.Body)
		n := requests.Add(1)
		switch n {
		case 1:
			emitNativeTaskTool(w, "standalone-write", "Write", map[string]any{"file_path": target, "content": "ACTUAL_STANDALONE_GO"})
		case 2:
			emitNativeTaskTool(w, "standalone-read", "Read", map[string]any{"file_path": target})
		case 3:
			if !strings.Contains(string(body), "ACTUAL_STANDALONE_GO") {
				t.Error("shared Harness omitted physical read result")
			}
			emitPlanFixture(w, 1, "", "STANDALONE_DONE")
		default:
			t.Error("extra query", n)
		}
	}))
	defer upstream.Close()
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
		target = filepath.Join(n.CWD, "standalone-result.txt")
		config := n.Environment["CLAUDE_CONFIG_DIR"]
		providers := map[string]any{"activeId": "standalone-fixture", "providers": []any{map[string]any{"id": "standalone-fixture", "presetId": "", "name": "Standalone", "baseUrl": upstream.URL, "apiKey": "private-proxy-fixture", "apiFormat": "anthropic", "authStrategy": "auth_token", "supportsNestedToolResultMedia": false, "models": map[string]string{"main": "fixture-model", "haiku": "fixture-model", "sonnet": "fixture-model", "opus": "fixture-model"}, "future": true}}, "futureIndex": true}
		if err := os.MkdirAll(filepath.Join(config, "cc-haha"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(config, "cc-haha", "providers.json"), rawMain(providers), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"enabledPlugins":{"sample@mkt":false},"network":{"proxy":{"mode":"system"},"aiRequestTimeoutMs":34567},"futureNetwork":{"preserve":true}}`), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(n.CWD, ".claude", "settings.json"), []byte(`{"network":{"aiRequestTimeoutMs":60000}}`), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Write,Read"} })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	userSettings, err := m.standaloneProxy.userSettings.GetUserSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if normalized := networksettings.Normalize(rawMain(userSettings)); normalized.TimeoutMillis != 34567 {
		t.Fatal("standalone network settings used merged project schema", normalized)
	}
	if string(userSettings["futureNetwork"]) != `{"preserve":true}` {
		t.Fatal("raw unknown user network field lost")
	}
	var endpoint string
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		endpoint = m.nativeOpenAIEnvironment()["ANTHROPIC_BASE_URL"]
		u, err := url.Parse(endpoint)
		if err != nil || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Port() == "3456" || u.Path != "/proxy" {
			return fmt.Errorf("real random standalone endpoint %q", endpoint)
		}
		value, err := m.nativeMergeCcHahaEnvironment(protocol.NewJSONObject())
		if err == nil && value.String("ANTHROPIC_BASE_URL") != endpoint {
			return fmt.Errorf("settings replay created another standalone port")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Actual standalone Write and Read"})})
	if err := m.RunTurn(ctx, user); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 3 {
		t.Fatal("not actual same Harness upstream requests", requests.Load())
	}
	if raw, err := os.ReadFile(target); err != nil || string(raw) != "ACTUAL_STANDALONE_GO" {
		t.Fatal("physical tools missing", string(raw), err)
	}
	response, err := http.Get(strings.TrimSuffix(endpoint, "/proxy") + "/health")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 200 || !strings.Contains(string(raw), `"status":"ok"`) {
		t.Fatal("actual standalone health", response.StatusCode, string(raw))
	}
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 300 * time.Millisecond}
	if response, err := client.Get(strings.TrimSuffix(endpoint, "/proxy") + "/health"); err == nil {
		response.Body.Close()
		t.Fatal("standalone listener escaped MainPrint shutdown")
	}
}
