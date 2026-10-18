package clibootstrap

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/coder/websocket"
)

func TestTerminalConfigAutoConnectSavedPreferenceDrivesFreshActualIDEAndSharedToolContinuation(t *testing.T) {
	var connects, toolCalls, modelCalls atomic.Int32
	closed := make(chan websocket.StatusCode, 1)
	ide := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"mcp"}})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		connects.Add(1)
		if r.Header.Get("X-Claude-Code-Ide-Authorization") != "saved-config-owned-ide-token" {
			t.Error("real IDE lock token not consumed")
		}
		for {
			_, data, err := conn.Read(r.Context())
			if err != nil {
				closed <- websocket.CloseStatus(err)
				return
			}
			input := mainObj(data)
			if input.Get("id") == nil {
				continue
			}
			result := json.RawMessage(`{}`)
			switch input.String("method") {
			case "initialize":
				result = []byte(`{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"saved-owned-IDE","version":"1"}}`)
			case "tools/list":
				result = []byte(`{"tools":[{"name":"getDiagnostics","description":"Saved real IDE action","inputSchema":{"type":"object","properties":{}}}]}`)
			case "tools/call":
				if mainObj(input.Get("params")).String("name") != "getDiagnostics" {
					t.Error(string(data))
				}
				toolCalls.Add(1)
				result = []byte(`{"content":[{"type":"text","text":"SAVED_IDE_ACTUAL_DIAGNOSTICS_564"}]}`)
			default:
				t.Error("unexpected IDE method", input.String("method"))
			}
			reply := protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quoteMain("2.0")}, protocol.JSONField{Name: "id", Value: input.Get("id")}, protocol.JSONField{Name: "result", Value: result})
			if err = conn.Write(r.Context(), websocket.MessageText, rawMain(reply)); err != nil {
				return
			}
		}
	}))
	defer ide.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		index := modelCalls.Add(1)
		if index == 1 {
			if !strings.Contains(string(mainObj(body).Get("tools")), "mcp__ide__getDiagnostics") {
				t.Error("actual fresh Host did not consume saved IDE catalog")
			}
			emitNativeTaskTool(w, "saved-ide-diagnostics", "mcp__ide__getDiagnostics", map[string]any{})
		} else {
			if !strings.Contains(string(mainObj(body).Get("messages")), "SAVED_IDE_ACTUAL_DIAGNOSTICS_564") {
				t.Error("physical result absent from same Harness continuation")
			}
			emitPlanFixture(w, int(index), "", "SAVED_IDE_SAME_HARNESS_DONE_564")
		}
	}))
	defer provider.Close()
	port, _ := strconv.Atoi(strings.TrimPrefix(ide.URL, "http://127.0.0.1:"))
	m := backgroundAgentMainWithOptions(t, provider.URL, func(n *NativeBoundary) {
		n.Environment["CLAUDE_CODE_IDE_SKIP_AUTO_INSTALL"] = "1"
		delete(n.Environment, "TERM_PROGRAM")
		path := filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "ide", strconv.Itoa(port)+".lock")
		_ = os.MkdirAll(filepath.Dir(path), 0700)
		_ = os.WriteFile(path, rawMain(map[string]any{"workspaceFolders": []string{n.CWD}, "pid": os.Getpid(), "transport": "ws", "ideName": "Saved Fixture IDE", "authToken": "saved-config-owned-ide-token"}), 0600)
		_ = os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], ".claude.json"), rawMain(map[string]any{"hasCompletedOnboarding": true, "autoConnectIde": false, "futureIDEGlobal": map[string]any{"keep": true}}), 0600)
	}, func(o *MainPrintOptions) {
		o.Interactive = true
		o.Arguments.IDE = false
		o.Arguments.Tools = []string{"Read"}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	select {
	case <-m.ideIntegration.searchDone:
	case <-ctx.Done():
		t.Fatal("real initial IDE search not joined")
	}
	if connects.Load() != 0 {
		t.Fatal("saved off value ignored before UI change")
	}
	if err := m.SetTerminalConfigSetting(ctx, "autoConnectIde", []byte("true")); err != nil {
		t.Fatal(err)
	}
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	next, err := NewMainPrint(m.options)
	if err != nil {
		t.Fatal(err)
	}
	defer next.ShutdownSession(context.Background())
	if err = next.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	for {
		ready := false
		err = next.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			_, tools, err := next.mcpTools.Catalog(scope)
			if err != nil {
				return err
			}
			for _, tool := range tools {
				ready = ready || tool.Name == "mcp__ide__getDiagnostics"
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if ready {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("saved on value not consumed by actual next startup", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "session_id", Value: quoteMain(next.Controller.SessionID())}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": "Use the actual saved IDE tool"})})
	if err = next.RunTurn(ctx, user); err != nil {
		t.Fatal(err)
	}
	if err = next.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-closed:
		if code != websocket.StatusNormalClosure {
			t.Fatal("native IDE not gracefully joined", code)
		}
	case <-ctx.Done():
		t.Fatal("saved IDE transport close not joined")
	}
	global, err := next.Global.Get()
	if err != nil {
		t.Fatal(err)
	}
	if connects.Load() != 1 || toolCalls.Load() != 1 || modelCalls.Load() != 2 || string(mainObj(global.Get("futureIDEGlobal")).Get("keep")) != "true" {
		t.Fatal(connects.Load(), toolCalls.Load(), modelCalls.Load(), global)
	}
}

func TestTerminalConfigChromeSavedOnEntitledCachedAccountInstallsActualNativeHostAtNextStartup(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("owned native POSIX Chrome wrapper")
	}
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
		delete(n.Environment, "ANTHROPIC_API_KEY")
		n.Environment["NODE_ENV"] = "production"
		n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
			return rawMain(map[string]any{"claudeAiOauth": map[string]any{"accessToken": "never-real-cached-chrome-fixture", "expiresAt": 2000000000000, "scopes": []string{"user:profile", "user:inference"}, "subscriptionType": "pro"}}), nil
		}
		_ = os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], ".claude.json"), rawMain(map[string]any{"hasCompletedOnboarding": true, "claudeInChromeDefaultEnabled": false, "futureChromeGlobal": map[string]any{"keep": true}}), 0600)
	}, func(o *MainPrintOptions) { o.Interactive = true; o.Platform = "darwin" })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if m.nativeDynamicMCP.Get("claude-in-chrome") != nil {
		t.Fatal("initial saved off ignored")
	}
	if err := m.SetTerminalConfigSetting(ctx, "claudeInChromeDefaultEnabled", []byte("true")); err != nil {
		t.Fatal(err)
	}
	if err := m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	next, err := NewMainPrint(m.options)
	if err != nil {
		t.Fatal(err)
	}
	defer next.ShutdownSession(context.Background())
	if err = next.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	next.joinNativeChromeInstallation()
	server := mainObj(next.nativeDynamicMCP.Get("claude-in-chrome"))
	if next.chromeInstallation == nil || server.String("type") != "stdio" || !strings.Contains(string(server.Get("args")), "--claude-in-chrome-mcp") || !strings.Contains(next.options.Arguments.AppendSystemPrompt, nativeChromePrompt) {
		t.Fatal("saved entitled startup did not consume actual native integration", server)
	}
	wrapper := filepath.Join(next.options.Native.Environment["CLAUDE_CONFIG_DIR"], "chrome", "chrome-native-host")
	body, err := os.ReadFile(wrapper)
	if err != nil || !strings.Contains(string(body), "--chrome-native-host") {
		t.Fatal("actual native host missing", err, string(body))
	}
	for _, base := range chromeBrowserDirectories(next.options.Native.Home, "darwin") {
		data, err := os.ReadFile(filepath.Join(base, "NativeMessagingHosts", "com.anthropic.claude_code_browser_extension.json"))
		if err != nil || mainObj(data).String("path") != wrapper {
			t.Fatal("actual browser manifest missing", err, string(data))
		}
	}
	global, err := next.Global.Get()
	if err != nil || string(mainObj(global.Get("futureChromeGlobal")).Get("keep")) != "true" {
		t.Fatal(global, err)
	}
}
