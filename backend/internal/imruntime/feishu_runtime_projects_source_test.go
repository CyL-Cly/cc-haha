package imruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/net/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestActualFeishuIndexProjectButtonsAmbiguityNewAndBusySessionRestore(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "repo")
	if e := os.MkdirAll(work, 0700); e != nil {
		t.Fatal(e)
	}
	now := int64(1770000000000)
	config := map[string]any{"defaultProjectDir": work, "allowedProjectRoots": []string{work}, "feishu": map[string]any{"appId": "fixture-feishu", "appSecret": "fixture-secret", "allowedUsers": []string{"U1"}}}
	message := func(user, chatType, id, text string) map[string]any {
		return map[string]any{"kind": "message", "event": map[string]any{"sender": map[string]any{"sender_id": map[string]string{"open_id": user}}, "message": map[string]any{"message_id": id, "chat_id": "chat-1", "chat_type": chatType, "message_type": "text", "content": string(raw(map[string]string{"text": text}))}}}
	}
	server := func(m any) map[string]any { return map[string]any{"kind": "server", "message": m} }
	card := func(path, name string) map[string]any {
		return map[string]any{"kind": "card", "event": map[string]any{"context": map[string]string{"open_chat_id": "chat-1"}, "action": map[string]any{"value": map[string]any{"action": "pick_project", "realPath": path, "projectName": name}}}}
	}
	alpha, alpine := filepath.Join(work, "alpha"), filepath.Join(work, "alpine")
	for _, dir := range []string{alpha, alpine} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	msg := func(id, text string) map[string]any { return message("U1", "p2p", id, text) }
	actions := []map[string]any{msg("projects", "/projects"), card(alpine, "alpine"), msg("notfound", "/new nonexistent"), msg("ambiguous", "/new alp"), msg("exact", "/new alpha"), msg("sessions", "/sessions projects"), msg("projectselection", "1"), server(map[string]any{"type": "status", "state": "thinking"}), msg("busyresume", "/resume 1"), server(map[string]any{"type": "message_complete"}), msg("resume", "/resume 1"), msg("default", "/new"), server(map[string]any{"type": "permission_request", "requestId": "fallback", "toolName": "Bash", "input": map[string]any{"command": "printf '<private>'", "unknown": true}}), {"kind": "card", "event": map[string]any{"context": map[string]string{"open_chat_id": "chat-1"}, "action": map[string]any{"value": map[string]any{"action": "permit", "requestId": "fallback", "allowed": "false"}}}}, server(map[string]any{"type": "permission_request", "requestId": "stale", "toolName": "Read", "input": map[string]string{"file_path": alpha + "/data.txt"}}), server(map[string]any{"type": "system_notification", "subtype": "init", "data": map[string]string{"model": "old-model"}}), server(map[string]any{"type": "error", "message": "Invalid signature in thinking block"})}
	run := func(source bool) (json.RawMessage, json.RawMessage) {
		var mu sync.Mutex
		packets := []any{}
		httpPackets := []any{}
		expected := 1
		creates := 0
		connections := []string{}
		allFrames := make(chan struct{})
		var framesOnce sync.Once
		ws := websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: func(c *websocket.Conn) {
			defer c.Close()
			for {
				var m protocol.JSONObject
				if websocket.JSON.Receive(c, &m) != nil {
					return
				}
				mu.Lock()
				packets = append(packets, m)
				n := len(packets)
				mu.Unlock()
				if n == expected {
					framesOnce.Do(func() { close(allFrames) })
				}
			}
		}}
		peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/ws/") {
				mu.Lock()
				connections = append(connections, r.URL.Path)
				mu.Unlock()
				ws.ServeHTTP(w, r)
				return
			}
			p, _ := io.ReadAll(r.Body)
			mu.Lock()
			packet := map[string]any{"path": r.URL.RequestURI(), "method": r.Method}
			if len(p) > 0 {
				packet["body"] = json.RawMessage(p)
			}
			httpPackets = append(httpPackets, packet)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/open-apis/auth/v3/tenant_access_token/internal":
				_, _ = w.Write([]byte(`{"tenant_access_token":"private-token","expire":3600}`))
			case "/open-apis/im/v1/messages":
				_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
			case "/api/sessions/session-1/git-info":
				_, _ = w.Write(raw(map[string]any{"repoName": "repo", "workDir": work, "branch": "main"}))
			case "/api/tasks/lists/session-1":
				_, _ = w.Write([]byte(`{"tasks":[{"id":"t","subject":"private","status":"in_progress"}]}`))
			case "/api/sessions/session-1":
				_, _ = w.Write(raw(map[string]any{"workDir": work, "permissionMode": "default"}))
			case "/api/sessions/recent-projects":
				_, _ = w.Write(raw(map[string]any{"projects": []any{map[string]any{"projectName": "alpha", "realPath": alpha, "branch": "main"}, map[string]any{"projectName": "alpine", "realPath": alpine, "branch": "topic"}}}))
			case "/api/sessions":
				if r.Method == "POST" {
					creates++
					_, _ = w.Write(raw(map[string]any{"sessionId": fmt.Sprintf("new-%d", creates)}))
				} else {
					_, _ = w.Write(raw(map[string]any{"total": 1, "sessions": []any{map[string]any{"id": "old-alpha", "title": "old\nalpha", "workDir": alpha, "projectRoot": alpha, "modifiedAt": "2026-02-01T00:00:00Z", "messageCount": 7, "permissionMode": "default", "workDirExists": true}}}))
				}
			case "/api/sessions/old-alpha":
				_, _ = w.Write(raw(map[string]any{"workDir": alpha, "permissionMode": "default"}))
			default:
				if strings.HasPrefix(r.URL.Path, "/api/sessions/new-") {
					_, _ = w.Write(raw(map[string]any{"workDir": work, "permissionMode": "default"}))
					return
				}
				t.Errorf("unowned runtime endpoint %s", r.URL.RequestURI())
				_, _ = w.Write([]byte(`{}`))
			}
		}))
		defer peer.Close()
		serverURL := "ws" + strings.TrimPrefix(peer.URL, "http")
		var result json.RawMessage
		if source {
			result = runPlatformStageSource(t, "native-feishu-runtime.fixture.ts", "CC_HAHA_FEISHU_RUNTIME_FIXTURE", map[string]any{"config": config, "peer": peer.URL, "server": serverURL, "now": now, "work": work, "seed": true, "actions": actions}, home)
		} else {
			configDir := filepath.Join(home, ".claude")
			if e := os.WriteFile(filepath.Join(configDir, "adapters.json"), raw(config), 0600); e != nil {
				t.Fatal(e)
			}
			cfg := LoadPlatformConfig("feishu", home, work, map[string]string{"CLAUDE_CONFIG_DIR": configDir, "ADAPTER_SERVER_URL": serverURL}, func(string) {})
			client := feishuSourceClient(t, peer, func() time.Time { return time.UnixMilli(now) }, "feishu")
			runtime, e := NewFeishuRuntime(context.Background(), FeishuRuntimeOptions{Config: cfg, Client: client, ServerHTTP: peer.Client(), Now: func() time.Time { return time.UnixMilli(now) }})
			if e != nil {
				t.Fatal(e)
			}
			defer runtime.Close()
			if e = runtime.Store.Set("chat-1", "session-1", work); e != nil {
				t.Fatal(e)
			}
			runtime.Bridge.Connect("chat-1", "session-1", runtime.handler("chat-1"))
			if !runtime.Bridge.WaitOpen(context.Background(), "chat-1") {
				t.Fatal("actual native session websocket did not open")
			}
			rows := []any{}
			for _, a := range actions {
				var value json.RawMessage
				var e error
				switch a["kind"] {
				case "message":
					e = runtime.HandleMessage(object(raw(a["event"])))
					if e == nil {
						e = runtime.Drain(context.Background(), "chat-1")
					}
				case "server":
					e = runtime.HandleServerMessage("chat-1", object(raw(a["message"])))
				case "card":
					value, e = runtime.HandleCardAction(object(raw(a["event"])))
				}
				errorText := ""
				if e != nil {
					errorText = e.Error()
				}
				s := runtime.State("chat-1")
				state := map[string]any{"state": s.State, "pendingPermissionCount": s.PendingPermissionCount, "pending": s.Pending}
				if s.Verb != "" {
					state["verb"] = s.Verb
				}
				if s.Model != "" {
					state["model"] = s.Model
				}
				if value == nil {
					value = raw(nil)
				}
				rows = append(rows, map[string]any{"error": errorText, "value": value, "state": state})
			}
			runtime.wg.Wait()
			result = raw(map[string]any{"rows": rows, "sessions": runtime.Store.List()})
		}

		select {
		case <-allFrames:
		case <-time.After(time.Second):
			t.Fatal("actual raw-valued permission frame missing")
		}
		mu.Lock()
		receipt := raw(map[string]any{"http": httpPackets, "frames": packets, "connections": connections})
		mu.Unlock()
		return result, receipt
	}
	want, wantPackets := run(true)
	got, gotPackets := run(false)
	exactJSON(t, got, want, "actual original Feishu project selection and busy session restore")
	exactJSON(t, gotPackets, wantPackets, "actual original Feishu project cards, fallback permissions and physical session connections")
}
