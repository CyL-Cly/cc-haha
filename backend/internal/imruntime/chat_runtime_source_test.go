package imruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/net/websocket"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type traceResponse struct {
	id     string
	n      int
	mu     *sync.Mutex
	events *[]any
}

func (s *traceResponse) Append(text Text) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	*s.events = append(*s.events, map[string]any{"id": s.id, "response": s.n, "append": text})
	return nil
}
func (s *traceResponse) Finish() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	*s.events = append(*s.events, map[string]any{"id": s.id, "response": s.n, "finish": true})
	return nil
}
func TestActualOriginalSharedIMRuntimeCommandsStreamPermissionsDeferredAttachmentsAndPhysicalBridge(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "repo")
	configDir := filepath.Join(home, ".claude")
	_ = os.MkdirAll(work, 0700)
	_ = os.MkdirAll(configDir, 0700)
	work, _ = filepath.EvalSymlinks(work)
	seed := map[string]any{"slack": map[string]any{"botToken": "private-bot", "appToken": "private-app", "allowedUsers": []string{"U1"}}, "defaultProjectDir": work}
	now := time.UnixMilli(1770000000000)
	actions := []map[string]any{}
	in := func(user, text, key string, media bool, refs any) {
		actions = append(actions, map[string]any{"kind": "inbound", "user": user, "text": text, "key": key, "media": media, "refs": refs})
	}
	msg := func(m map[string]any) { actions = append(actions, map[string]any{"kind": "server", "message": m}) }
	in("stranger", "/help", "deny", true, []any{})
	in("U1", " /help ", "help", false, nil)
	in("U1", "hello", "first", false, nil)
	in("U1", "ignored duplicate", "first", true, []any{})
	msg(map[string]any{"type": "system_notification", "subtype": "init", "data": map[string]string{"model": " model-X "}})
	msg(map[string]any{"type": "status", "state": "thinking", "verb": "Thinking"})
	msg(map[string]any{"type": "content_start", "blockType": "text"})
	msg(map[string]any{"type": "content_delta", "text": "before tool"})
	msg(map[string]any{"type": "content_start", "blockType": "tool_use", "toolName": "Write"})
	msg(map[string]any{"type": "tool_use_complete", "toolName": "Write"})
	msg(map[string]any{"type": "permission_request", "requestId": "approve", "toolName": "Write", "input": map[string]string{"file_path": "/private/a", "content": "x"}})
	in("U1", "2", "allow", false, nil)
	msg(map[string]any{"type": "permission_resolved", "permissionType": "tool", "requestId": "approve"})
	msg(map[string]any{"type": "tool_result"})
	msg(map[string]any{"type": "content_delta", "text": "after approval"})
	msg(map[string]any{"type": "message_complete"})
	in("U1", "/status", "status", false, nil)
	in("U1", "/stop", "stop", false, nil)
	in("U1", "/clear", "clear", false, nil)
	msg(map[string]any{"type": "permission_requests_snapshot", "toolRequestIds": []string{"x", "y", "x"}, "turnActive": true})
	in("U1", "/deny x", "deny-perm", false, nil)
	msg(map[string]any{"type": "permission_resolved", "permissionType": "tool", "requestId": "y"})
	in("U1", "caption", "file", true, []any{map[string]string{"type": "image", "name": "a.png", "data": "AQID", "mimeType": "image/png"}})
	msg(map[string]any{"type": "content_delta", "text": "visible"})
	actions = append(actions, map[string]any{"kind": "clear"})
	msg(map[string]any{"type": "content_delta", "text": "second response"})
	msg(map[string]any{"type": "error", "message": "plain failure"})
	msg(map[string]any{"type": "error", "message": "Invalid signature in thinking block"})
	in("U1", "/projects", "projects", false, nil)
	in("U1", "1", "pick", false, nil)
	in("U1", "/new missing", "missing", false, nil)
	in("U1", "/cancel", "cancel", false, nil)
	want := runIMSource(t, "native-im-chat-runtime.fixture.ts", "CC_HAHA_IM_CHAT_RUNTIME_FIXTURE", map[string]any{"work": work, "config": seed, "now": now.UnixMilli(), "actions": actions}, home)
	_ = os.WriteFile(filepath.Join(configDir, "adapters.json"), raw(seed), 0600)
	_ = os.Remove(filepath.Join(configDir, "adapter-sessions.json"))
	var mu sync.Mutex
	notices, streams, packets, httpCalls, clears, busy := []any{}, []any{}, []any{}, []any{}, []any{}, []any{}
	nextID, responseID, downloads := 0, 0, 0
	ws := websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: func(conn *websocket.Conn) {
		defer conn.Close()
		id := strings.TrimPrefix(conn.Request().URL.Path, "/ws/")
		for {
			var m protocol.JSONObject
			if websocket.JSON.Receive(conn, &m) != nil {
				return
			}
			mu.Lock()
			packets = append(packets, map[string]any{"id": id, "message": m})
			mu.Unlock()
		}
	}}
	projects := []any{map[string]any{"projectName": "repo", "realPath": work, "projectPath": work, "branch": "main"}}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/ws/") {
			ws.ServeHTTP(w, r)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		var data any
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/sessions":
			var m protocol.JSONObject
			_ = json.NewDecoder(r.Body).Decode(&m)
			httpCalls = append(httpCalls, map[string]any{"method": "create", "work": m.String("workDir")})
			nextID++
			data = map[string]any{"sessionId": fmt.Sprintf("session-%d", nextID)}
		case r.URL.Path == "/api/sessions/recent-projects":
			httpCalls = append(httpCalls, map[string]any{"method": "projects"})
			data = map[string]any{"projects": projects}
		case strings.HasSuffix(r.URL.Path, "/git-info"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/git-info")
			httpCalls = append(httpCalls, map[string]any{"method": "git", "id": id})
			data = map[string]any{"repoName": "repo", "branch": "main", "workDir": work}
		case strings.HasPrefix(r.URL.Path, "/api/tasks/lists/"):
			id := strings.TrimPrefix(r.URL.Path, "/api/tasks/lists/")
			httpCalls = append(httpCalls, map[string]any{"method": "tasks", "id": id})
			data = map[string]any{"tasks": []any{map[string]string{"status": "pending"}, map[string]string{"status": "in_progress"}, map[string]string{"status": "completed"}}}
		default:
			id := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
			httpCalls = append(httpCalls, map[string]any{"method": "exists", "id": id})
			data = map[string]any{"workDir": work, "permissionMode": "default"}
		}
		_, _ = w.Write(raw(data))
	}))
	defer peer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := Config{Home: home, ConfigDir: configDir, WorkDir: work, Roots: []string{home}, ServerURL: "ws" + strings.TrimPrefix(peer.URL, "http"), Environment: map[string]string{}}
	bridge := NewBridge(ctx, cfg, nil, nil)
	defer bridge.Close()
	store := NewSessionStore(configDir)
	store.NowMillis = func() int64 { return now.UnixMilli() }
	pair := NewPairing(configDir, nil)
	dedup := NewMessageDedup(ctx)
	defer dedup.Close()
	runtime := NewChatRuntime(ctx, ChatRuntimeOptions{Config: cfg, Bridge: bridge, Store: store, Client: NewHTTPClient(cfg, peer.Client()), Pairing: pair, Dedup: dedup, FlushThreshold: 1, FlushInterval: time.Minute, Port: ChatPort{Platform: "slack", LogPrefix: "[Slack]", SendNotice: func(id, text string) error {
		mu.Lock()
		notices = append(notices, map[string]any{"id": id, "text": text})
		mu.Unlock()
		return nil
	}, SetBusy: func(id string, value bool) {
		mu.Lock()
		busy = append(busy, map[string]any{"id": id, "value": value})
		mu.Unlock()
	}, ClearChat: func(id string) { mu.Lock(); clears = append(clears, id); mu.Unlock() }, CreateResponse: func(id string) ResponseStream {
		mu.Lock()
		responseID++
		n := responseID
		mu.Unlock()
		return &traceResponse{id, n, &mu, &streams}
	}}})
	defer runtime.Close()
	states := []any{}
	for _, a := range actions {
		kind := a["kind"].(string)
		switch kind {
		case "inbound":
			media := a["media"].(bool)
			refs := collectJSONArray(raw(a["refs"]))
			_ = runtime.HandleInbound(InboundChatMessage{ChatID: "D1", UserID: raw(a["user"]), DisplayName: "Private User", DedupKey: a["key"].(string), Text: a["text"].(string), HasAttachments: &media, LoadAttachments: func() ([]protocol.JSONObject, error) { downloads++; return refs, nil }})
		case "server":
			m := object(raw(a["message"]))
			if err := runtime.HandleServerMessage("D1", m); err != nil {
				t.Fatal(err)
			}
			if m.String("type") == "content_delta" {
				runtime.getBuffer("D1").Complete()
			}
		case "clear":
			runtime.ClearChat("D1")
		}
		runtime.wg.Wait()
		s := runtime.State("D1")
		state := map[string]any{"state": s.State, "pendingPermissionCount": s.PendingPermissionCount}
		if s.Verb != "" {
			state["verb"] = s.Verb
		}
		if s.Model != "" {
			state["model"] = s.Model
		}
		states = append(states, state)
	}
	// Physical WebSocket packets arrive after the sender returns.
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		n := len(packets)
		mu.Unlock()
		if n >= 6 || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	binding, _ := os.ReadFile(store.Path)
	mu.Lock()
	got := raw(map[string]any{"notices": notices, "streams": streams, "packets": packets, "http": httpCalls, "clears": clears, "busy": busy, "downloads": downloads, "states": states, "bindings": json.RawMessage(binding)})
	mu.Unlock()
	exactJSON(t, got, want, "shared actual Source + physical HTTP/WS")
}
