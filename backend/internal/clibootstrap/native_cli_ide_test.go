package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/coder/websocket"
)

func TestNativeCLIIDEActualLockfileOwnerCatalogueToolContinuationAndJoinedClose(t *testing.T) {
	var mu sync.Mutex
	methods := []string{}
	headers := []http.Header{}
	notices := []protocol.JSONObject{}
	closed := make(chan websocket.StatusCode, 1)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, e := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"mcp"}})
		if e != nil {
			return
		}
		defer conn.CloseNow()
		mu.Lock()
		headers = append(headers, r.Header.Clone())
		mu.Unlock()
		for {
			_, data, e := conn.Read(r.Context())
			if e != nil {
				closed <- websocket.CloseStatus(e)
				return
			}
			var input protocol.JSONObject
			_ = input.UnmarshalJSON(data)
			mu.Lock()
			methods = append(methods, input.String("method"))
			if input.Get("id") == nil {
				notices = append(notices, input.Clone())
			}
			mu.Unlock()
			if input.Get("id") == nil {
				continue
			}
			result := json.RawMessage(`{}`)
			switch input.String("method") {
			case "initialize":
				result = []byte(`{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"actual-owned-IDE","version":"1"}}`)
			case "tools/list":
				result = []byte(`{"tools":[{"name":"getDiagnostics","description":"Actual IDE loopback action","inputSchema":{"type":"object","properties":{"input":{"type":"string"}},"required":["input"]}}]}`)
			case "tools/call":
				params := nativeInProcessObject(input.Get("params"))
				args := nativeInProcessObject(params.Get("arguments"))
				if params.String("name") != "getDiagnostics" || args.String("input") != "IDE_TOOL_INPUT" {
					t.Error("wrong physical IDE call", string(data))
				}
				result = []byte(`{"content":[{"type":"text","text":"IDE_ACTUAL_ACTION_OK 中文"}]}`)
			default:
				t.Error("unexpected IDE method", input.String("method"))
				return
			}
			reply := protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: quoteMain("2.0")}, protocol.JSONField{Name: "id", Value: input.Get("id")}, protocol.JSONField{Name: "result", Value: result})
			if e = conn.Write(r.Context(), websocket.MessageText, rawMain(reply)); e != nil {
				return
			}
		}
	}))
	defer peer.Close()
	var requests []protocol.JSONObject
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var input protocol.JSONObject
		_ = input.UnmarshalJSON(data)
		mu.Lock()
		requests = append(requests, input.Clone())
		index := len(requests)
		mu.Unlock()
		if index == 1 {
			if !strings.Contains(string(input.Get("tools")), `mcp__ide__getDiagnostics`) {
				t.Error("IDE action absent from actual next provider request", string(data))
			}
			emitNativeTaskTool(w, "owned-ide-call", "mcp__ide__getDiagnostics", map[string]string{"input": "IDE_TOOL_INPUT"})
		} else {
			if !strings.Contains(string(input.Get("messages")), "IDE_ACTUAL_ACTION_OK 中文") {
				t.Error("IDE tool result absent from actual continuation", string(data))
			}
			emitPlanFixture(w, index, "", "IDE_HARNESS_CONTINUED")
		}
	}))
	defer model.Close()
	portText := strings.TrimPrefix(peer.URL, "http://127.0.0.1:")
	port, _ := strconv.Atoi(portText)
	var diagnostics bytes.Buffer
	m := backgroundAgentMainWithOptions(t, model.URL, func(n *NativeBoundary) {
		n.Environment["CLAUDE_CODE_IDE_SKIP_AUTO_INSTALL"] = "1"
		path := filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "ide", strconv.Itoa(port)+".lock")
		_ = os.MkdirAll(filepath.Dir(path), 0700)
		data := rawMain(map[string]any{"workspaceFolders": []string{n.CWD}, "pid": os.Getpid(), "ideName": "Private IDE 中文", "transport": "ws", "authToken": "private-loopback-ide-token"})
		if e := os.WriteFile(path, data, 0600); e != nil {
			t.Fatal(e)
		}
	}, func(o *MainPrintOptions) {
		o.Interactive = true
		o.Arguments.IDE = true
		o.Arguments.Tools = []string{"Read"}
		o.PID = 170123
		o.Error = &diagnostics
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	lastStatus := ""
	for {
		ready := false
		e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			status, _ := m.mcpTools.Status(scope)
			lastStatus = string(rawMain(status)) + " dynamic=" + string(rawMain(m.nativeDynamicMCP))
			if m.mcpTools == nil {
				return nil
			}
			_, tools, e := m.mcpTools.Catalog(scope)
			names := []string{}
			for _, tool := range tools {
				names = append(names, tool.Name)
			}
			lastStatus += " tools=" + strings.Join(names, ",")
			if e != nil {
				return e
			}
			for _, tool := range tools {
				if tool.Name == "mcp__ide__getDiagnostics" {
					ready = true
				}
			}
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		if ready {
			break
		}
		select {
		case <-ctx.Done():
			mu.Lock()
			trace := append([]string{}, methods...)
			mu.Unlock()
			t.Fatal("IDE action was not adopted by same Owner", lastStatus, trace, diagnostics.String(), "discovery", m.ideIntegration)
		case <-time.After(10 * time.Millisecond):
		}
	}
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "session_id", Value: quoteMain(m.Controller.SessionID())}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": "Use the discovered IDE action"})})
	if e := m.RunTurn(ctx, user); e != nil {
		t.Fatal(e)
	}
	if e := m.ShutdownSession(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case code := <-closed:
		if code != websocket.StatusNormalClosure {
			t.Fatal("IDE transport was not gracefully closed", code)
		}
	case <-ctx.Done():
		t.Fatal("IDE socket was not joined")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(headers) != 1 || headers[0].Get("X-Claude-Code-Ide-Authorization") != "private-loopback-ide-token" || len(requests) != 2 {
		t.Fatal("IDE shared runtime/authorization/continuation mismatch", len(headers), len(requests), methods)
	}
	found := false
	for _, n := range notices {
		if n.String("method") == "ide_connected" {
			params := nativeInProcessObject(n.Get("params"))
			found = string(params.Get("pid")) == "170123"
		}
	}
	if !found {
		t.Fatal("Source IDE-connected actual PID notification missing", notices)
	}
}

func TestNativeCLIIDEExplicitFalsyGateAndSearchCloseNeverConnectOrStartModel(t *testing.T) {
	for _, row := range []struct {
		name                    string
		flag, interactive, lock bool
		env                     string
	}{{"disabled-even-flag", true, true, true, "false"}, {"unset-default", false, true, true, ""}, {"headless-flag-ignored", true, false, true, ""}, {"no-lock-joined-search", true, true, false, ""}} {
		t.Run(row.name, func(t *testing.T) {
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("inactive IDE or model unexpectedly connected")
				w.WriteHeader(500)
			}))
			defer peer.Close()
			m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
				if row.env != "" {
					n.Environment["CLAUDE_CODE_AUTO_CONNECT_IDE"] = row.env
				}
				if row.lock {
					path := filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "ide", strings.TrimPrefix(peer.URL, "http://127.0.0.1:")+".lock")
					_ = os.MkdirAll(filepath.Dir(path), 0700)
					_ = os.WriteFile(path, rawMain(map[string]any{"workspaceFolders": []string{n.CWD}, "pid": os.Getpid(), "transport": "ws"}), 0600)
				}
			}, func(o *MainPrintOptions) { o.Interactive = row.interactive; o.Arguments.IDE = row.flag })
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if m.ideIntegration != nil && row.lock {
				select {
				case <-m.ideIntegration.searchDone:
				case <-ctx.Done():
					t.Fatal("found IDE search did not finish")
				}
			}
			if e := m.Owner.Run(ctx, func(*cooperative.Scope) error {
				if m.nativeDynamicMCP.Get("ide") != nil {
					t.Error("Source explicit auto-connect gate ignored")
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			start := time.Now()
			if e := m.ShutdownSession(ctx); e != nil {
				t.Fatal(e)
			}
			if time.Since(start) > time.Second {
				t.Fatal("empty IDE discovery blocked runtime shutdown")
			}
		})
	}
}
