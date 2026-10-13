package application

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"golang.org/x/net/websocket"
)

func TestActualMCPManagementToggleChangesNextDesktopInference(t *testing.T) {
	var toolCalls, modelCalls, securityCalls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		data, _ := io.ReadAll(r.Body)
		message := obj(data)
		if message.Get("id") == nil {
			w.WriteHeader(202)
			return
		}
		var result any
		switch message.String("method") {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "actual-toggle-peer", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "actual", "description": "Return actual side effect count.", "inputSchema": map[string]any{"type": "object"}}}}
		case "tools/call":
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "actual side effect"}}, "structuredContent": map[string]any{"count": toolCalls.Add(1)}}
		default:
			result = map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw(map[string]any{"jsonrpc": "2.0", "id": message.Get("id"), "result": result}))
	}))
	defer peer.Close()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		tools := obj(data).Get("tools")
		switch modelCalls.Add(1) {
		case 1, 4:
			if !bytes.Contains(tools, []byte("mcp__owned__actual")) {
				t.Error("enabled MCP absent from actual model request")
			}
			response(w, "native-toggle", "mcp__owned__actual", map[string]any{}, "")
		case 3:
			if bytes.Contains(tools, []byte("mcp__owned__actual")) {
				t.Error("disabled MCP survived actual model request")
			}
			response(w, "native-disabled", "", nil, "disabled correctly")
		default:
			response(w, "native-complete", "", nil, "tool completed")
		}
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Arguments.Tools = []string{""}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"mcpServers": map[string]any{"owned": map[string]any{"type": "http", "url": peer.URL}}}), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: config, WorkDir: o.WorkDir, Arguments: o.Arguments, Version: o.Version, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) {
		securityCalls.Add(1)
		return nil, os.ErrPermission
	}, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	request := func(path string, value any) protocol.JSONObject {
		t.Helper()
		r, _ := http.NewRequest("POST", host.URL+path, bytes.NewReader(raw(value)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer fake-only-loopback")
		response, err := host.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != 200 && response.StatusCode != 201 {
			t.Fatalf("actual HTTP %d %s", response.StatusCode, data)
		}
		return obj(data)
	}
	created := request("/api/sessions", map[string]any{"workDir": o.WorkDir, "permissionMode": "bypassPermissions"})
	id := created.String("sessionId")
	wsConfig, err := websocket.NewConfig("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+id, host.URL)
	if err != nil {
		t.Fatal(err)
	}
	wsConfig.Header.Set("Authorization", "Bearer fake-only-loopback")
	ws, err := websocket.DialConfig(wsConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = ws.SetDeadline(time.Now().Add(15 * time.Second))
	turn := func() {
		t.Helper()
		if err := websocket.Message.Send(ws, string(raw(map[string]any{"type": "user_message", "content": "Use available MCP tool; if disabled report it."}))); err != nil {
			t.Fatal(err)
		}
		seen := false
		for i := 0; i < 100; i++ {
			var frame string
			if err := websocket.Message.Receive(ws, &frame); err != nil {
				t.Fatal(err)
			}
			message := obj([]byte(frame))
			if message.String("type") == "error" {
				t.Fatal("actual shared Harness error", frame)
			}
			if message.String("type") == "message_complete" {
				seen = true
				requestDesktopSessionSync(t, ws)
			}
			if seen && message.String("type") == "session_state" {
				assertDesktopIdleSnapshot(t, message)
				return
			}
		}
		t.Fatal("actual turn did not finish")
	}
	turn()
	for _, expected := range []string{"disabled", "connected"} {
		toggled := request("/api/mcp/owned/toggle", map[string]any{"cwd": o.WorkDir, "sessionId": id})
		if obj(toggled.Get("server")).String("status") != expected || string(obj(toggled.Get("sessionSync")).Get("applied")) != "true" {
			t.Fatalf("toggle did not update actual live session: %s", raw(toggled))
		}
		turn()
	}
	if modelCalls.Load() != 5 || toolCalls.Load() != 2 || securityCalls.Load() == 0 {
		t.Fatal("shared endpoint/credential/tool path was not exercised", modelCalls.Load(), toolCalls.Load(), securityCalls.Load())
	}
}
