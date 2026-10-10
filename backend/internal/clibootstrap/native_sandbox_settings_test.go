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
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeSandboxActualSettingsWatcherUpdatesPolicyDuringSameActiveASK(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var modelCalls, targetCalls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls.Add(1)
		close(started)
		select {
		case <-release:
			emitPlanFixture(w, 1, "", "SANDBOX_SETTINGS_SAME_ASK_FINISHED")
		case <-r.Context().Done():
		}
	}))
	defer peer.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls.Add(1)
		io.WriteString(w, "real warm proxy policy bytes")
	}))
	defer target.Close()
	errors := make(chan string, 10)
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		n.Environment["PATH"] = "/opt/homebrew/bin:/usr/bin:/bin"
		n.Environment["CLAUDE_CODE_TMPDIR"] = n.Temp
		if e := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"sandbox":{"enabled":true,"network":{"allowedDomains":[]}}}`), 0600); e != nil {
			t.Fatal(e)
		}
	}, func(o *MainPrintOptions) {
		o.Arguments.Tools = nil
		o.Arguments.AllowedTools = nil
		o.Arguments.PermissionMode = "default"
		o.OnEnvironmentHookMessage = func(text string, warning bool) {
			if warning {
				errors <- text
			}
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Keep this actual ASK open while settings change"})}))
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	port, socks, e := m.NativeSandboxProxyPorts(ctx)
	if e != nil {
		t.Fatal(e)
	}
	proxy, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}, Timeout: 2 * time.Second}
	defer client.CloseIdleConnections()
	request := func() int {
		t.Helper()
		response, e := client.Get(target.URL)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		io.Copy(io.Discard, response.Body)
		return response.StatusCode
	}
	if status := request(); status != 403 {
		t.Fatal("initial closed SDK permission", status)
	}
	settingsPath := filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "settings.json")
	if e = os.WriteFile(settingsPath, []byte(`{"sandbox":{"enabled":true,"network":{"allowedDomains":["127.0.0.1"]}},"future":{"keep":true}}`), 0600); e != nil {
		t.Fatal(e)
	}
	for {
		updated := false
		receipt, e := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
			value, e := m.Startup.GetInitialSettings()
			if e == nil {
				sandbox, _ := value.Object("sandbox")
				network, _ := sandbox.Object("network")
				updated = strings.Contains(string(network.Get("allowedDomains")), "127.0.0.1")
			}
			return e
		})
		if e != nil || receipt.Wait() != nil {
			t.Fatal(e)
		}
		if updated {
			break
		}
		select {
		case warning := <-errors:
			t.Fatal("actual watcher rejected active continuation", warning)
		case <-ctx.Done():
			t.Fatal("actual watcher did not refresh warm settings", ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	if status := request(); status != 200 {
		t.Fatal("hot update did not replace live network policy", status)
	}
	next, nextSocks, e := m.NativeSandboxProxyPorts(ctx)
	if e != nil || next != port || nextSocks != socks {
		t.Fatal("settings reload restarted proxy listeners", port, socks, next, nextSocks, e)
	}
	if modelCalls.Load() != 1 || targetCalls.Load() != 1 {
		t.Fatal("watcher changed model/target count", modelCalls.Load(), targetCalls.Load())
	}
	close(release)
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if e = m.ShutdownSession(context.Background()); e != nil {
		t.Fatal(e)
	}
}
