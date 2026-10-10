package imruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/net/websocket"
)

func TestActualWechatEntrypointStreamPermissionErrorCommandsAndPhysicalServerBridge(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "repo")
	configDir := filepath.Join(home, ".claude")
	for _, dir := range []string{work, configDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	seed := map[string]any{"defaultProjectDir": work, "wechat": map[string]any{"accountId": "private-account", "botToken": "private-token", "allowedUsers": []string{"U1"}}}
	now := time.UnixMilli(1770000000000)
	actions := []map[string]any{}
	server := func(m map[string]any) { actions = append(actions, map[string]any{"kind": "server", "message": m}) }
	inbound := func(user, text, key string) {
		actions = append(actions, map[string]any{"kind": "inbound", "user": user, "text": text, "key": key})
	}
	inbound("stranger", "/help", "unauthorized")
	inbound("U1", "/help", "help")
	server(map[string]any{"type": "system_notification", "subtype": "init", "data": map[string]string{"model": " model-X "}})
	server(map[string]any{"type": "status", "state": "thinking", "verb": "Thinking"})
	server(map[string]any{"type": "content_start", "blockType": "text"})
	server(map[string]any{"type": "content_delta", "text": "buffer before permission"})
	server(map[string]any{"type": "permission_request", "requestId": "approve", "toolName": "Write", "input": map[string]string{"file_path": "/private/fixture.txt", "content": "fixture"}})
	inbound("U1", "/allow approve", "allow")
	server(map[string]any{"type": "permission_resolved", "permissionType": "tool", "requestId": "approve"})
	server(map[string]any{"type": "tool_result"})
	server(map[string]any{"type": "content_delta", "text": " after approval"})
	server(map[string]any{"type": "message_complete"})
	server(map[string]any{"type": "content_delta", "text": "discard this"})
	server(map[string]any{"type": "error", "message": "Invalid signature in thinking block"})
	server(map[string]any{"type": "content_delta", "text": " \t\n"})
	server(map[string]any{"type": "message_complete"})
	inbound("U1", "新会话 repo", "literal-chinese-argument")
	inbound("U1", "ignored duplicate", "literal-chinese-argument")
	inbound("U1", "/stop", "stop")
	inbound("U1", "/clear", "clear")
	server(map[string]any{"type": "permission_requests_snapshot", "toolRequestIds": []string{"x", "y", "x"}, "turnActive": true})
	inbound("U1", "/deny x", "deny")
	server(map[string]any{"type": "permission_resolved", "permissionType": "tool", "requestId": "y"})
	server(map[string]any{"type": "content_delta", "text": "最后的内容"})
	actions = append(actions, map[string]any{"kind": "clear"})
	server(map[string]any{"type": "message_complete"})
	want := runPlatformStageSource(t, "native-wechat-runtime.fixture.ts", "CC_HAHA_WECHAT_RUNTIME_FIXTURE", map[string]any{"config": seed, "work": work, "now": now.UnixMilli(), "actions": actions}, home)
	if err := os.WriteFile(filepath.Join(configDir, "adapters.json"), raw(seed), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	notices, packets, httpCalls := []any{}, []any{}, []any{}
	ws := websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: func(conn *websocket.Conn) {
		defer conn.Close()
		for {
			var message protocol.JSONObject
			if websocket.JSON.Receive(conn, &message) != nil {
				return
			}
			mu.Lock()
			packets = append(packets, map[string]any{"id": "session-1", "message": message})
			mu.Unlock()
		}
	}}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasPrefix(req.URL.Path, "/ws/") {
			ws.ServeHTTP(w, req)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/ilink/bot/sendmessage":
			var body protocol.JSONObject
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			message := object(body.Get("msg"))
			var items []protocol.JSONObject
			_ = json.Unmarshal(message.Get("item_list"), &items)
			if len(items) != 1 {
				t.Error("missing outgoing text item")
				return
			}
			mu.Lock()
			notices = append(notices, map[string]any{"id": message.Get("to_user_id"), "text": object(items[0].Get("text_item")).Get("text")})
			mu.Unlock()
			_, _ = w.Write([]byte(`{"ret":0}`))
		case "/ilink/bot/getconfig":
			_, _ = w.Write([]byte(`{"ret":0}`))
		default:
			mu.Lock()
			httpCalls = append(httpCalls, map[string]string{"path": req.URL.Path})
			mu.Unlock()
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer peer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := LoadPlatformConfig("wechat", home, work, map[string]string{"CLAUDE_CONFIG_DIR": configDir, "WECHAT_BASE_URL": peer.URL, "ADAPTER_SERVER_URL": "ws" + strings.TrimPrefix(peer.URL, "http")}, func(string) {})
	runtime, err := NewWechatRuntime(ctx, WechatRuntimeOptions{Config: cfg, ServerHTTP: peer.Client(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := runtime.Store.Set("U1", "session-1", work); err != nil {
		t.Fatal(err)
	}
	runtime.Bridge.Connect("U1", "session-1", runtime.Chat.handler("U1"))
	if !runtime.Bridge.WaitOpen(ctx, "U1") {
		t.Fatal("physical session websocket did not open")
	}
	states := []any{}
	for _, action := range actions {
		switch action["kind"] {
		case "server":
			if err := runtime.Chat.HandleServerMessage("U1", object(raw(action["message"]))); err != nil {
				t.Fatal(err)
			}
		case "inbound":
			message := map[string]any{"from_user_id": action["user"], "message_id": action["key"], "item_list": []any{map[string]any{"type": 1, "text_item": map[string]any{"text": action["text"]}}}}
			if err := runtime.HandleMessage(raw(message)); err != nil {
				t.Fatal(err)
			}
			runtime.Chat.wg.Wait()
		case "clear":
			runtime.Chat.ClearChat("U1")
			runtime.Chat.wg.Wait()
		}
		state := runtime.Chat.State("U1")
		value := map[string]any{"state": state.State, "pendingPermissionCount": state.PendingPermissionCount}
		if state.Verb != "" {
			value["verb"] = state.Verb
		}
		if state.Model != "" {
			value["model"] = state.Model
		}
		states = append(states, value)
	}
	var expected struct {
		Packets []json.RawMessage `json:"packets"`
	}
	_ = json.Unmarshal(want, &expected)
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		n := len(packets)
		mu.Unlock()
		if n >= len(expected.Packets) || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	got := raw(map[string]any{"notices": notices, "packets": packets, "states": states, "http": httpCalls})
	mu.Unlock()
	exactJSON(t, got, want, "actual Wechat entrypoint and native HTTP/WS")
}
