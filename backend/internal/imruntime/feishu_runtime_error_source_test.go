package imruntime

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/net/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestActualFeishuIndexMissingErrorMessageAbortsExistingCardWithSourceUnknownError(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "repo")
	if e := os.MkdirAll(work, 0700); e != nil {
		t.Fatal(e)
	}
	now := int64(1770000000000)
	config := map[string]any{"defaultProjectDir": work, "allowedProjectRoots": []string{work}, "feishu": map[string]any{"appId": "fixture-feishu", "appSecret": "fixture-secret", "allowedUsers": []string{"U1"}}}
	content := string(raw(map[string]any{"zh_cn": map[string]any{"content": [][]any{{map[string]string{"tag": "text", "text": "@_user_12 /help "}, map[string]string{"tag": "img", "image_key": "image-private"}, map[string]string{"tag": "file", "file_key": "file-private", "file_name": "private.txt"}, map[string]string{"tag": "file", "file_key": "missing", "file_name": "missing.txt"}}}}}))
	actions := []map[string]any{{"kind": "message", "event": map[string]any{"sender": map[string]any{"sender_id": map[string]string{"open_id": "U1"}}, "message": map[string]any{"message_id": "message-with-media", "chat_id": "chat-1", "chat_type": "p2p", "message_type": "post", "content": content}}}, {"kind": "ready"}, {"kind": "server", "message": map[string]any{"type": "thinking", "text": "private reasoning"}}, {"kind": "server", "message": map[string]any{"type": "content_start", "blockType": "tool_use", "toolName": "Read", "toolUseId": "tool-id"}}, {"kind": "server", "message": map[string]any{"type": "tool_use_complete", "toolName": "Read", "toolUseId": "tool-id"}}, {"kind": "server", "message": map[string]any{"type": "content_delta", "text": "# Final Answer\nprivate 中文 🧡"}}, {"kind": "server", "message": map[string]any{"type": "error"}}}
	run := func(source bool) (json.RawMessage, json.RawMessage) {
		var mu sync.Mutex
		packets, httpPackets := []any{}, []any{}
		received := make(chan struct{})
		var once sync.Once
		ws := websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: func(c *websocket.Conn) {
			defer c.Close()
			for {
				var m protocol.JSONObject
				if websocket.JSON.Receive(c, &m) != nil {
					return
				}
				mu.Lock()
				packets = append(packets, m)
				mu.Unlock()
				once.Do(func() { close(received) })
			}
		}}
		peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/ws/") {
				ws.ServeHTTP(w, r)
				return
			}
			p, _ := io.ReadAll(r.Body)
			packet := map[string]any{"path": r.URL.RequestURI(), "method": r.Method}
			if len(p) > 0 {
				packet["body"] = json.RawMessage(p)
			}
			mu.Lock()
			httpPackets = append(httpPackets, packet)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/open-apis/auth/v3/tenant_access_token/internal":
				_, _ = w.Write([]byte(`{"tenant_access_token":"private-token","expire":3600}`))
			case "/open-apis/im/v1/messages":
				_, _ = w.Write([]byte(`{"code":0,"data":{"message_id":"card-message"}}`))
			case "/open-apis/cardkit/v1/cards":
				_, _ = w.Write([]byte(`{"code":0,"data":{"card_id":"card-private"}}`))
			case "/open-apis/im/v1/messages/message-with-media/resources/image-private":
				_, _ = w.Write([]byte("private-image"))
			case "/open-apis/im/v1/messages/message-with-media/resources/file-private":
				_, _ = w.Write([]byte("private-file"))
			case "/open-apis/im/v1/messages/message-with-media/resources/missing":
				w.WriteHeader(404)
				_, _ = w.Write([]byte(`{"msg":"private missing"}`))
			default:
				_, _ = w.Write([]byte(`{"code":0}`))
			}
		}))
		defer peer.Close()
		serverURL := "ws" + strings.TrimPrefix(peer.URL, "http")
		var result json.RawMessage
		if source {
			result = runPlatformStageSource(t, "native-feishu-runtime.fixture.ts", "CC_HAHA_FEISHU_RUNTIME_FIXTURE", map[string]any{"config": config, "peer": peer.URL, "server": serverURL, "now": now, "work": work, "seed": true, "actions": actions}, home)
		} else {
			if e := os.RemoveAll(filepath.Join(home, ".claude", "im-downloads")); e != nil {
				t.Fatal(e)
			}
			configDir := filepath.Join(home, ".claude")
			if e := os.WriteFile(filepath.Join(configDir, "adapters.json"), raw(config), 0600); e != nil {
				t.Fatal(e)
			}
			cfg := LoadPlatformConfig("feishu", home, work, map[string]string{"CLAUDE_CONFIG_DIR": configDir, "ADAPTER_SERVER_URL": serverURL}, func(string) {})
			client := feishuSourceClient(t, peer, func() time.Time { return time.UnixMilli(now) }, "feishu")
			runtime, e := NewFeishuRuntime(context.Background(), FeishuRuntimeOptions{Config: cfg, Client: client, ServerHTTP: peer.Client(), Now: func() time.Time { return time.UnixMilli(now) }, Streaming: FeishuStreamingOptions{UUID: func() string { return "fixture-runtime-uuid" }}})
			if e != nil {
				t.Fatal(e)
			}
			defer runtime.Close()
			_ = runtime.Store.Set("chat-1", "session-1", work)
			runtime.Bridge.Connect("chat-1", "session-1", runtime.handler("chat-1"))
			if !runtime.Bridge.WaitOpen(context.Background(), "chat-1") {
				t.Fatal("native physical session socket missing")
			}
			rows := []any{}
			for _, a := range actions {
				var e error
				switch a["kind"] {
				case "message":
					e = runtime.HandleMessage(object(raw(a["event"])))
					_ = runtime.Drain(context.Background(), "chat-1")
				case "ready":
					runtime.wg.Wait()
				case "server":
					e = runtime.HandleServerMessage("chat-1", object(raw(a["message"])))
				}
				s := runtime.State("chat-1")
				state := map[string]any{"state": s.State, "pendingPermissionCount": s.PendingPermissionCount, "pending": s.Pending}
				if s.Verb != "" {
					state["verb"] = s.Verb
				}
				if s.Model != "" {
					state["model"] = s.Model
				}
				errText := ""
				if e != nil {
					errText = e.Error()
				}
				rows = append(rows, map[string]any{"error": errText, "value": nil, "state": state})
			}
			runtime.wg.Wait()
			result = raw(map[string]any{"rows": rows, "sessions": runtime.Store.List()})
		}
		select {
		case <-received:
		case <-time.After(time.Second):
			t.Fatal("media message did not reach actual SessionWS")
		}
		mu.Lock()
		receipt := raw(map[string]any{"http": httpPackets, "frames": packets})
		mu.Unlock()
		return result, receipt
	}
	want, wantPackets := run(true)
	got, gotPackets := run(false)
	exactJSON(t, got, want, "original Feishu index attachments and precreated card state")
	normalize := func(p json.RawMessage) json.RawMessage {
		v := object(p)
		var httpCalls []protocol.JSONObject
		_ = json.Unmarshal(v.Get("http"), &httpCalls)
		downloads := []protocol.JSONObject{}
		ordered := []protocol.JSONObject{}
		for _, call := range httpCalls {
			if strings.Contains(call.String("path"), "/resources/") {
				downloads = append(downloads, call)
			} else {
				ordered = append(ordered, call)
			}
		}
		sort.Slice(downloads, func(i, j int) bool { return downloads[i].String("path") < downloads[j].String("path") })
		v.Set("http", raw(ordered))
		v.Set("downloads", raw(downloads))
		return raw(v)
	}
	exactJSON(t, normalize(gotPackets), normalize(wantPackets), "original Feishu allSettled media and actual stream/final HTTP consumers")
}
