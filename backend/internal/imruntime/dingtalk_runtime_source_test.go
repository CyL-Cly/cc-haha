package imruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/net/websocket"
)

type dingRuntimeRedirect struct {
	target *url.URL
	next   http.RoundTripper
}

func (d dingRuntimeRedirect) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	u := *req.URL
	u.Scheme, u.Host = d.target.Scheme, d.target.Host
	copy.URL = &u
	return d.next.RoundTrip(copy)
}

func TestActualDingTalkOriginalIndexCardPermissionStateAndPhysicalBridge(t *testing.T) {
	for _, profile := range []string{"no-permission-card", "permission-card", "card-create-fallback"} {
		t.Run(profile, func(t *testing.T) {
			home := t.TempDir()
			work := filepath.Join(home, "repo")
			configDir := filepath.Join(home, ".claude")
			for _, dir := range []string{work, configDir} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			work, _ = filepath.EvalSymlinks(work)
			seed := map[string]any{"defaultProjectDir": work, "dingtalk": map[string]any{"clientId": "private-id", "clientSecret": "private-secret", "allowedUsers": []string{"U1"}}}
			if profile == "permission-card" {
				seed["dingtalk"].(map[string]any)["permissionCardTemplateId"] = " private-template "
			}
			now := time.UnixMilli(1770000000000)
			actions := []map[string]any{}
			server := func(m map[string]any) { actions = append(actions, map[string]any{"kind": "server", "message": m}) }
			inbound := func(user, text string) {
				actions = append(actions, map[string]any{"kind": "inbound", "user": user, "text": text})
			}
			inbound("stranger", "/help")
			inbound("U1", "/help")
			server(map[string]any{"type": "system_notification", "subtype": "init", "data": map[string]string{"model": " model-X "}})
			server(map[string]any{"type": "status", "state": "thinking", "verb": "Thinking"})
			server(map[string]any{"type": "content_start", "blockType": "tool_use", "toolName": "Read"})
			server(map[string]any{"type": "tool_use_complete", "toolName": "Read"})
			server(map[string]any{"type": "tool_result"})
			server(map[string]any{"type": "content_start", "blockType": "text"})
			server(map[string]any{"type": "content_delta", "text": "before permission"})
			server(map[string]any{"type": "permission_request", "requestId": "approve", "toolName": "Write", "input": map[string]string{"file_path": "/private/fixture", "content": "fixture"}})
			inbound("U1", "/allow approve")
			server(map[string]any{"type": "permission_resolved", "permissionType": "tool", "requestId": "approve"})
			server(map[string]any{"type": "content_delta", "text": "after permission\n| X | Y |\n| --- | --- |\n| a | b |"})
			server(map[string]any{"type": "message_complete"})
			server(map[string]any{"type": "content_delta", "text": "discard"})
			server(map[string]any{"type": "error", "message": "Invalid signature in thinking block"})
			server(map[string]any{"type": "message_complete"})
			inbound("U1", "/status")
			inbound("U1", "/stop")
			inbound("U1", "/clear")
			server(map[string]any{"type": "permission_requests_snapshot", "toolRequestIds": []string{"x", "y", "x"}, "turnActive": true})
			actions = append(actions, map[string]any{"kind": "card", "raw": map[string]any{"chatId": "dingtalk:dm:U1", "requestId": "x", "allowed": false}})
			server(map[string]any{"type": "permission_resolved", "permissionType": "tool", "requestId": "y"})
			server(map[string]any{"type": "content_delta", "text": " \t\n"})
			server(map[string]any{"type": "message_complete"})
			inbound("U1", "/projects")
			inbound("U1", "/help")
			inbound("U1", "/new missing")
			inbound("U1", "新会话 repo")
			server(map[string]any{"type": "content_delta", "text": "last"})
			actions = append(actions, map[string]any{"kind": "clear"})
			server(map[string]any{"type": "message_complete"})
			var mu sync.Mutex
			wire, packets := []any{}, []any{}
			packetCh := make(chan struct{}, 100)
			ws := websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: func(conn *websocket.Conn) {
				defer conn.Close()
				for {
					var message protocol.JSONObject
					if websocket.JSON.Receive(conn, &message) != nil {
						return
					}
					mu.Lock()
					packets = append(packets, map[string]any{"id": strings.TrimPrefix(conn.Request().URL.Path, "/ws/"), "message": message})
					mu.Unlock()
					packetCh <- struct{}{}
				}
			}}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if strings.HasPrefix(req.URL.Path, "/ws/") {
					ws.ServeHTTP(w, req)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if req.URL.Path == "/reply" || strings.HasPrefix(req.URL.Path, "/v1.0/") {
					var body protocol.JSONObject
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						t.Error(err)
						return
					}
					mu.Lock()
					wire = append(wire, map[string]any{"method": req.Method, "path": req.URL.Path, "body": body})
					mu.Unlock()
					if req.URL.Path == "/v1.0/oauth2/accessToken" {
						_, _ = w.Write([]byte(`{"accessToken":"private-access","expireIn":7200}`))
						return
					}
					if profile == "card-create-fallback" && req.URL.Path == "/v1.0/card/instances" && req.Method == "POST" {
						w.WriteHeader(500)
						_, _ = w.Write([]byte("private create failure"))
						return
					}
					_, _ = w.Write([]byte(`{}`))
					return
				}
				switch req.URL.Path {
				case "/api/sessions/recent-projects":
					_, _ = w.Write([]byte(`{"projects":[]}`))
				case "/api/sessions":
					_, _ = w.Write([]byte(`{"sessionId":"created-session"}`))
				default:
					_, _ = w.Write(raw(map[string]any{"workDir": work, "permissionMode": "default", "repoName": "repo", "branch": "main", "tasks": []any{}}))
				}
			}))
			defer peer.Close()
			want := runPlatformStageSource(t, "native-dingtalk-runtime.fixture.ts", "CC_HAHA_DINGTALK_RUNTIME_FIXTURE", map[string]any{"config": seed, "work": work, "now": now.UnixMilli(), "actions": actions, "peer": peer.URL}, home)
			mu.Lock()
			sourceWire := append([]any{}, wire...)
			wire = []any{}
			mu.Unlock()
			if err := os.WriteFile(filepath.Join(configDir, "adapters.json"), raw(seed), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cfg := LoadPlatformConfig("dingtalk", home, work, map[string]string{"CLAUDE_CONFIG_DIR": configDir, "ADAPTER_SERVER_URL": "ws" + strings.TrimPrefix(peer.URL, "http")}, func(string) {})
			u, _ := url.Parse(peer.URL)
			apiHTTP := &http.Client{Transport: dingRuntimeRedirect{u, peer.Client().Transport}}
			r, err := NewDingTalkRuntime(ctx, DingTalkRuntimeOptions{Config: cfg, APIOptions: DingTalkAPIOptions{HTTP: apiHTTP, Random: func() float64 { return .125 }}, ServerHTTP: peer.Client(), Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			if err := r.Store.Set("dingtalk:dm:U1", "session-1", work); err != nil {
				t.Fatal(err)
			}
			r.Bridge.Connect("dingtalk:dm:U1", "session-1", r.handler("dingtalk:dm:U1"))
			if !r.Bridge.WaitOpen(ctx, "dingtalk:dm:U1") {
				t.Fatal("physical bridge open")
			}
			states, snapshots := []any{}, []any{}
			for _, action := range actions {
				switch action["kind"] {
				case "server":
					if err := r.HandleServerMessage("dingtalk:dm:U1", object(raw(action["message"]))); err != nil {
						t.Fatal(err)
					}
				case "inbound":
					if err := r.HandleRobotMessage(raw(map[string]any{"conversationType": "1", "senderStaffId": action["user"], "senderNick": "tester", "sessionWebhook": peer.URL + "/reply", "msgtype": "text", "text": map[string]any{"content": action["text"]}})); err != nil {
						t.Fatal(err)
					}
					r.wg.Wait()
				case "clear":
					r.ClearChat("dingtalk:dm:U1")
				case "card":
					if err := r.HandleCardCallback(raw(action["raw"])); err != nil {
						t.Fatal(err)
					}
				}
				state := r.State("dingtalk:dm:U1")
				value := map[string]any{"state": state.State, "pendingPermissionCount": state.PendingPermissionCount}
				if state.Verb != "" {
					value["verb"] = state.Verb
				}
				if state.Model != "" {
					value["model"] = state.Model
				}
				states = append(states, value)
				cards, texts, buffers, permissions, permissionChats := []any{}, []any{}, []any{}, []any{}, []any{}
				c := r.chat("dingtalk:dm:U1")
				c.mu.Lock()
				if c.card != nil {
					cards = append(cards, "dingtalk:dm:U1")
				}
				if c.textPresent {
					texts = append(texts, []any{"dingtalk:dm:U1", c.text.Clone()})
				}
				if c.buffer != nil {
					buffers = append(buffers, "dingtalk:dm:U1")
				}
				if c.state.Pending != nil {
					permissions = append(permissions, []any{"dingtalk:dm:U1", append([]string{}, c.state.Pending...)})
				}
				c.mu.Unlock()
				r.mu.Lock()
				for key, id := range r.permissionChats {
					permissionChats = append(permissionChats, []any{key, id})
				}
				r.mu.Unlock()
				snapshots = append(snapshots, map[string]any{"cards": cards, "texts": texts, "buffers": buffers, "permissions": permissions, "permissionChats": permissionChats})
			}
			var expected struct {
				Packets []json.RawMessage `json:"packets"`
			}
			if err := json.Unmarshal(want, &expected); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < len(expected.Packets); i++ {
				select {
				case <-packetCh:
				case <-ctx.Done():
					t.Fatal("physical packet completion", ctx.Err())
				}
			}
			mu.Lock()
			got := raw(map[string]any{"packets": packets, "states": states, "snapshots": snapshots})
			goWire := append([]any{}, wire...)
			mu.Unlock()
			exactJSON(t, got, want, "actual original DingTalk index consumer state, approval and physical bridge")
			exactJSON(t, raw(goWire), raw(sourceWire), "actual original DingTalk index physical token/markdown/CardKit payloads")
		})
	}
}
