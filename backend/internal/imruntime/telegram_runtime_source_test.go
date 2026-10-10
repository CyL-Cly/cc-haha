package imruntime

import (
	"context"
	"encoding/json"
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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/net/websocket"
)

func TestOriginalTelegramEntrypointRealHandlersHTTPWSCommandsPermissionsAndState(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "work")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	work = existingDir(work, home)
	now := int64(1791404000000)
	projects := []any{map[string]any{"projectName": "Private Repo", "realPath": work, "projectPath": work, "branch": "fixture", "sessionCount": 1}}
	sessions := []any{map[string]any{"id": "history-session", "title": "History", "workDir": work, "workDirExists": true, "permissionMode": "default", "projectRoot": work, "modifiedAt": "2026-10-08T01:02:00.000Z", "messageCount": 2}}
	actions := []map[string]any{}
	messageID := 0
	addUser := func(text string, user int, kind string, command bool) {
		messageID++
		message := map[string]any{"message_id": messageID, "date": 1, "from": map[string]any{"id": user, "first_name": "Fixture", "is_bot": false}, "chat": map[string]any{"id": 42, "type": kind}, "text": text}
		if command {
			name := strings.Split(text, " ")[0]
			message["entities"] = []any{map[string]any{"offset": 0, "length": len(NewText(name)), "type": "bot_command"}}
		}
		actions = append(actions, map[string]any{"update": map[string]any{"update_id": messageID, "message": message}})
	}
	addEvent := func(v any) { actions = append(actions, map[string]any{"event": v}) }
	callbackID := 0
	addCallback := func(data string, user int) {
		callbackID++
		actions = append(actions, map[string]any{"update": map[string]any{"update_id": 100 + callbackID, "callback_query": map[string]any{"id": "callback-" + stringNumber(callbackID), "from": map[string]any{"id": user, "is_bot": false, "first_name": "Fixture"}, "message": map[string]any{"message_id": 99, "date": 1, "text": "original", "chat": map[string]any{"id": 42, "type": "private"}}, "data": data}}})
	}
	addUser("/help", 43, "group", true)
	addUser("hello", 43, "private", false)
	addUser("hello", 42, "group", false)
	addUser("hello", 42, "private", false)
	actions = append(actions, actions[len(actions)-1])
	addEvent(map[string]any{"type": "status", "state": "thinking", "verb": "Thinking"})
	addEvent(map[string]any{"type": "thinking", "text": " private reasoning"})
	addEvent(map[string]any{"type": "content_start", "blockType": "text"})
	addEvent(map[string]any{"type": "content_delta", "text": "hello streamed"})
	addEvent(map[string]any{"type": "content_start", "blockType": "tool_use"})
	addEvent(map[string]any{"type": "content_start", "blockType": "text"})
	addEvent(map[string]any{"type": "content_delta", "text": "after actual tool"})
	addEvent(map[string]any{"type": "message_complete"})
	addEvent(map[string]any{"type": "system_notification", "subtype": "init", "data": map[string]any{"model": "private-model"}})
	addEvent(map[string]any{"type": "permission_request", "requestId": "permission-1", "toolName": "Write", "input": map[string]any{"file_path": "private.txt", "content": "exact"}})
	addCallback("permit:permission-1:yes", 43)
	addCallback("permit:gone:no", 42)
	addCallback("permit:permission-1:always", 42)
	addEvent(map[string]any{"type": "permission_requests_snapshot", "turnActive": true})
	addEvent(map[string]any{"type": "permission_request", "requestId": "permission-2", "toolName": "Bash", "input": map[string]string{"command": "printf test"}})
	addUser("3", 42, "private", false)
	addEvent(map[string]any{"type": "permission_resolved", "permissionType": "tool", "requestId": "permission-2"})
	addEvent(map[string]any{"type": "permission_requests_snapshot", "toolRequestIds": []any{}, "turnActive": false})
	addUser("/status", 42, "private", true)
	addUser("/provider", 42, "private", true)
	addCallback("tgsel:provider:pick:2", 42)
	addUser("/model", 42, "private", true)
	addCallback("tgsel:model:pick:1", 42)
	addUser("/skills", 42, "private", true)
	addCallback("tgsel:skill:pick:0", 42)
	addEvent(map[string]any{"type": "message_complete"})
	addUser("/sessions", 42, "private", true)
	addUser("/cancel", 42, "private", false)
	addUser("/resume", 42, "private", true)
	addCallback("tgsel:resume_project:pick:0", 42)
	addCallback("tgsel:resume_session:pick:0", 42)
	addUser("/stop", 42, "private", true)
	addUser("/clear", 42, "private", true)
	addEvent(map[string]any{"type": "message_complete"})
	addUser("/new", 42, "private", true)
	addUser("/model", 42, "private", true)
	addCallback("tgsel:model:pick:0", 42)
	actions[len(actions)-1]["now"] = now + 900000
	addUser("/model", 42, "private", true)
	addCallback("tgsel:model:pick:0", 42)
	actions[len(actions)-1]["now"] = now + 1800001
	want := runIMSource(t, "native-telegram-entry-runtime.fixture.ts", "CC_HAHA_TELEGRAM_ENTRY_RUNTIME_FIXTURE", map[string]any{"now": now, "work": work, "projects": projects, "sessions": sessions, "actions": actions}, home)
	var mu sync.Mutex
	calls, requests, packets := []any{}, []any{}, []any{}
	nextMessage, nextSession := 0, 0
	active, currentModel := "private-provider", "private-model"
	ws := websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: func(conn *websocket.Conn) {
		defer conn.Close()
		id := strings.TrimPrefix(conn.Request().URL.Path, "/ws/")
		for {
			var message json.RawMessage
			if err := websocket.JSON.Receive(conn, &message); err != nil {
				return
			}
			mu.Lock()
			packets = append(packets, map[string]any{"id": id, "message": message})
			mu.Unlock()
		}
	}}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		path := request.URL.Path
		if strings.HasPrefix(path, "/ws/") {
			mu.Lock()
			requests = append(requests, map[string]any{"method": "WS", "path": path, "token": request.URL.Query().Get("token")})
			mu.Unlock()
			ws.ServeHTTP(w, request)
			return
		}
		bytes, _ := io.ReadAll(request.Body)
		body := object(bytes)
		mu.Lock()
		defer mu.Unlock()
		result := any(map[string]any{})
		if strings.HasPrefix(path, "/botprivate-token/") {
			method := path[strings.LastIndex(path, "/")+1:]
			calls = append(calls, map[string]any{"method": method, "payload": body})
			result = true
			if method == "sendMessage" {
				nextMessage++
				result = map[string]any{"message_id": nextMessage}
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(raw(map[string]any{"ok": true, "result": result}))
			return
		}
		record := map[string]any{"method": request.Method, "path": request.URL.RequestURI(), "authorization": request.Header.Get("Authorization")}
		if len(bytes) > 0 {
			record["body"] = body
		}
		requests = append(requests, record)
		switch {
		case path == "/api/sessions" && request.Method == "POST":
			nextSession++
			result = map[string]any{"sessionId": "private-session-" + stringNumber(nextSession)}
		case path == "/api/sessions/recent-projects":
			result = map[string]any{"projects": projects}
		case path == "/api/sessions":
			result = map[string]any{"sessions": sessions, "total": len(sessions)}
		case strings.HasSuffix(path, "/git-info"):
			result = map[string]any{"repoName": "Private Repo", "branch": "fixture", "workDir": work}
		case strings.HasPrefix(path, "/api/tasks/lists/"):
			result = map[string]any{"tasks": []any{map[string]any{"id": "1", "subject": "test", "status": "completed"}}}
		case strings.HasPrefix(path, "/api/sessions/"):
			result = map[string]any{"workDir": work, "permissionMode": "default"}
		case path == "/api/providers":
			result = map[string]any{"providers": []any{map[string]any{"id": "private-provider", "name": "Private", "models": map[string]string{"main": "private-model"}}}, "activeId": active}
		case strings.HasSuffix(path, "/activate"):
			active = strings.Split(path, "/")[3]
			result = map[string]any{"ok": true}
		case path == "/api/models":
			result = map[string]any{"provider": map[string]string{"id": active, "name": "Private"}, "models": []any{map[string]string{"id": "private-model", "name": "Private Model", "description": "fixture", "context": "test"}, map[string]string{"id": "second-model", "name": "Second", "description": "", "context": ""}}}
		case path == "/api/models/current":
			if request.Method == "PUT" {
				currentModel = body.String("modelId")
			}
			result = map[string]any{"model": map[string]string{"id": currentModel}}
		case path == "/api/skills":
			result = map[string]any{"skills": []any{map[string]any{"name": "private-skill", "displayName": "Private Skill", "source": "project", "description": "fixture", "userInvocable": true}, map[string]any{"name": "hidden", "source": "user", "description": "", "userInvocable": false}}}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw(result))
	}))
	defer peer.Close()
	private := t.TempDir()
	configDir := filepath.Join(private, ".claude")
	if err := os.Mkdir(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	seed := map[string]any{"defaultProjectDir": work, "allowedProjectRoots": []string{work}, "telegram": map[string]any{"botToken": "private-token", "allowedUsers": []int{42}}, "pairing": map[string]any{"code": "PAIRME", "expiresAt": now + 600000}}
	if err := os.WriteFile(filepath.Join(configDir, "adapters.json"), raw(seed), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"CLAUDE_CONFIG_DIR": configDir, "CC_HAHA_LOCAL_ACCESS_TOKEN": "private-server-token", "ADAPTER_SERVER_URL": "ws" + strings.TrimPrefix(peer.URL, "http")}
	config := LoadConfig(private, "/", env, func(string) {})
	api, err := NewTelegramAPI(TelegramAPIOptions{Token: "private-token", APIRoot: peer.URL, HTTP: peer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	clock := now
	runtime, err := NewTelegramRuntime(context.Background(), TelegramRuntimeOptions{Config: config, API: api, ServerHTTP: peer.Client(), MediaHTTP: peer.Client(), Now: func() time.Time { return time.UnixMilli(clock) }})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	runtime.Store.NowMillis = func() int64 { return now }
	runtime.Pairing.NowMillis = func() int64 { return now }
	runtime.Poller.username = "private_test_bot"
	rows := []any{}
	for _, action := range actions {
		if value, ok := action["now"]; ok {
			clock = value.(int64)
		}
		if update, ok := action["update"]; ok {
			if err = runtime.HandleUpdate(context.Background(), object(raw(update))); err != nil {
				t.Fatal(err)
			}
		}
		if event, ok := action["event"]; ok {
			if err = runtime.HandleServerMessage("42", object(raw(event))); err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(15 * time.Millisecond)
		c := runtime.chat("42")
		c.mu.Lock()
		state := map[string]any{"state": c.state.State, "pendingPermissionCount": c.state.PendingPermissionCount}
		if c.state.Model != "" {
			state["model"] = c.state.Model
		}
		if c.state.Verb != "" {
			state["verb"] = c.state.Verb
		}
		c.mu.Unlock()
		var stored any
		if data, e := os.ReadFile(runtime.Store.Path); e == nil {
			_ = json.Unmarshal(data, &stored)
		}
		mu.Lock()
		rows = append(rows, map[string]any{"calls": calls, "http": requests, "packets": packets, "state": state, "stored": stored})
		calls, requests, packets = []any{}, []any{}, []any{}
		mu.Unlock()
	}
	// Source issues the model list and current-model REST requests concurrently.
	// Compare that pair independently of their physical socket arrival order.
	var expected []protocol.JSONObject
	_ = json.Unmarshal(want, &expected)
	for i, row := range rows {
		actual := row.(map[string]any)
		canonical := func(requests []json.RawMessage) []json.RawMessage {
			if len(requests) == 2 {
				a, b := object(requests[0]).String("path"), object(requests[1]).String("path")
				if a == "/api/models" && b == "/api/models/current" || b == "/api/models" && a == "/api/models/current" {
					sort.Slice(requests, func(i, j int) bool { return object(requests[i]).String("path") < object(requests[j]).String("path") })
				}
			}
			return requests
		}
		var actualRequests, expectedRequests []json.RawMessage
		_ = json.Unmarshal(raw(actual["http"]), &actualRequests)
		_ = json.Unmarshal(expected[i].Get("http"), &expectedRequests)
		actual["http"] = canonical(actualRequests)
		expected[i].Set("http", raw(canonical(expectedRequests)))
	}
	exactJSON(t, raw(rows), raw(expected), "runtime")
}
