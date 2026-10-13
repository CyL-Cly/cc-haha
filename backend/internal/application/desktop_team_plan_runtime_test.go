package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamplan"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"golang.org/x/net/websocket"
)

func TestActualDesktopApprovedTeamRuntimeBarrierTasksQuestionIdleMailboxWakeAndShutdown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	var workspace string
	var prepared atomic.Int32
	var mu sync.Mutex
	rounds := map[string]int{}
	bodies := map[string][]json.RawMessage{}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		worker := "leader"
		for _, name := range []string{"one", "two"} {
			if bytes.Contains(body, []byte("APPROVED_WORKER_"+name)) {
				worker = name
				break
			}
		}
		mu.Lock()
		rounds[worker]++
		round := rounds[worker]
		bodies[worker] = append(bodies[worker], body)
		mu.Unlock()
		if worker == "leader" {
			messages := rowsForTeam(obj(body).Get("messages"))
			last := ""
			if len(messages) > 0 {
				last = string(messages[len(messages)-1].Get("content"))
			}
			if strings.Contains(last, "CLEANUP_ACTUAL_TEAM") {
				response(w, "leader-delete", "TeamDelete", map[string]any{}, "")
				return
			}
			response(w, "leader-ready", "", nil, "leader ready")
			return
		}
		if prepared.Load() != 2 {
			t.Error("model executed before all actual worker Harness instances were constructed", prepared.Load())
		}
		switch {
		case worker == "one" && round == 1:
			response(w, "one-question", "AskUserQuestion", map[string]any{"questions": []any{map[string]any{"question": "Approve real team?", "header": "Team", "options": []any{map[string]string{"label": "Go", "description": "Continue"}, map[string]string{"label": "Stop", "description": "Stop"}}, "multiSelect": false}}}, "")
		case worker == "one" && round == 2:
			if !bytes.Contains(body, []byte("Go")) {
				t.Error("actual same pending reply lost", string(body))
			}
			response(w, "one-write", "Write", map[string]any{"file_path": filepath.Join(workspace, "team-one.txt"), "content": "ACTUAL_TEAM_ONE_GO"}, "")
		case worker == "one" && round == 3:
			mu.Lock()
			downstream := rounds["two"]
			mu.Unlock()
			if downstream != 0 {
				t.Error("downstream model ran before prerequisite completed")
			}
			response(w, "one-handoff", "SendMessage", map[string]any{"to": "two", "message": "HANDOFF_ACTUAL_TEAM_ONE_GO", "summary": "Upstream task result"}, "")
		case worker == "one" && round == 4:
			if !bytes.Contains(body, []byte("Message sent to two's inbox")) {
				t.Error("handoff tool did not succeed", string(body))
			}
			response(w, "one-task-complete", "TaskUpdate", map[string]any{"taskId": "1", "status": "completed"}, "")
		case worker == "two" && round == 1:
			if !bytes.Contains(body, []byte("HANDOFF_ACTUAL_TEAM_ONE_GO")) || !bytes.Contains(body, []byte("Ready now (dependencies complete)")) {
				t.Error("actual downstream input missing handoff/ready instructions", string(body))
			}
			response(w, "two-task", "TaskGet", map[string]string{"taskId": "2"}, "")
		case worker == "two" && round == 2:
			if !bytes.Contains(body, []byte("shared second")) {
				t.Error("actual shared TaskGet missing", string(body))
			}
			response(w, "two-write", "Write", map[string]any{"file_path": filepath.Join(workspace, "team-two.txt"), "content": "ACTUAL_TEAM_TWO"}, "")
		case worker == "one" && round == 6:
			if !bytes.Contains(body, []byte("wake real same child")) || !bytes.Contains(body, []byte("teammate-message")) {
				t.Error("actual timer inbox lost", string(body))
			}
			response(w, "one-wake-read", "Read", map[string]string{"file_path": filepath.Join(workspace, "team-one.txt")}, "")
		case worker == "one" && round == 8 || worker == "two" && round == 4:
			requestID := regexp.MustCompile(`shutdown-[0-9]+@` + worker).FindString(string(body))
			if requestID == "" {
				t.Error("actual process shutdown input lost its request ID", worker, string(body))
				http.Error(w, "missing shutdown request", 500)
				return
			}
			response(w, worker+"-approve-shutdown", "SendMessage", map[string]any{"to": "team-lead", "message": map[string]any{"type": "shutdown_response", "request_id": requestID, "approve": true}}, "")
		default:
			response(w, worker+"-complete", "", nil, "actual team worker complete")
		}
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	workspace = o.WorkDir
	o.Arguments.Tools = []string{"Read,Write,AskUserQuestion,TaskCreate,TaskGet,TaskList,TaskUpdate,TeamDelete"}
	o.Environment["CLAUDE_CODE_ENABLE_TASKS"] = "1"
	o.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
	o.Environment["DISABLE_TELEMETRY"] = "1"
	o.Environment["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "true"
	if err := os.MkdirAll(o.Environment["CLAUDE_CONFIG_DIR"], 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"agentTeamsEnabled":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		native, err := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: filepath.Join(o.Environment["HOME"], "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
		if err == nil && env["CC_HAHA_TEAM_WORKER"] == "1" {
			prepared.Add(1)
		}
		return native, err
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	post := func(path string, v any) protocol.JSONObject {
		t.Helper()
		response, err := host.Client().Post(host.URL+path, "application/json", bytes.NewReader(raw(v)))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		wantStatus := 201
		if strings.HasSuffix(path, "/approve") {
			wantStatus = 200
		}
		if response.StatusCode != wantStatus {
			t.Fatal(response.StatusCode, string(data))
		}
		return obj(data)
	}
	providerID := obj(post("/api/providers", map[string]any{"presetId": "deepseek", "name": "Private actual team", "apiKey": "fake-team-key", "baseUrl": model.URL, "apiFormat": "anthropic", "models": map[string]string{"main": "fixture-model", "haiku": "", "sonnet": "", "opus": ""}}).Get("provider")).String("id")
	parent := post("/api/sessions", map[string]any{"workDir": workspace, "permissionMode": "bypassPermissions"}).String("sessionId")
	var ws *websocket.Conn
	send := func(v any) {
		t.Helper()
		if err := websocket.Message.Send(ws, string(raw(v))); err != nil {
			t.Fatal(err)
		}
	}
	receive := func(want string) protocol.JSONObject {
		t.Helper()
		for {
			var data string
			if err := websocket.Message.Receive(ws, &data); err != nil {
				t.Fatal(err)
			}
			v := obj([]byte(data))
			if v.String("type") == "error" {
				t.Fatal(data)
			}
			if v.String("type") == want {
				return v
			}
		}
	}
	configDir := o.Environment["CLAUDE_CONFIG_DIR"]
	tasks, _ := taskstore.New(taskstore.Options{ConfigDir: configDir, PID: os.Getpid(), Now: time.Now})
	config, _ := teamstore.NewConfig(teamstore.ConfigOptions{ConfigDir: configDir, TaskStore: tasks, PID: os.Getpid()})
	mailbox, _ := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: configDir, TaskStore: tasks})
	counter := 0
	plans, _ := teamplan.New(teamplan.Options{Config: config, Tasks: tasks, ConfigDir: configDir, WorkDir: workspace, Now: time.Now, UUID: func() (string, error) {
		counter++
		return "aaaaaaaa-aaaa-4aaa-8aaa-" + strings.Repeat("0", 11) + string(rune('0'+counter)), nil
	}})
	team := protocol.NewJSONObject(field("name", "actual-team"), field("leadSessionId", parent), field("leadAgentId", "team-lead@actual-team"), field("createdAt", 1000), field("members", []any{}))
	if err = config.Write("actual-team", team); err != nil {
		t.Fatal(err)
	}
	members := []protocol.JSONObject{}
	catalog := protocol.NewJSONObject()
	names := []string{"Read", "Write", "AskUserQuestion", "TaskGet"}
	for _, name := range []string{"one", "two"} {
		agent := "preset-" + name
		catalog.Set(agent, raw(protocol.NewJSONObject(field("source", "built-in"), field("sourceIdentity", map[string]string{"kind": "builtin"}), field("systemPrompt", "APPROVED_WORKER_"+name), field("tools", names))))
		members = append(members, protocol.NewJSONObject(field("id", name), field("name", name), field("agentType", agent), field("prompt", "Execute approved actual team task"), field("runtime", map[string]string{"providerId": providerID, "modelId": "fixture-model"})))
	}
	initial := protocol.NewJSONObject(field("agentCatalog", catalog), field("members", members), field("tasks", []protocol.JSONObject{protocol.NewJSONObject(field("id", "task-a"), field("subject", "shared first"), field("ownerId", "one"), field("dependencies", []string{})), protocol.NewJSONObject(field("id", "task-b"), field("subject", "shared second"), field("ownerId", "two"), field("dependencies", []string{"task-a"}))}))
	plan, err := plans.EnsureDraft("actual-team", parent, protocol.NewJSONObject(field("providerId", providerID), field("modelId", "fixture-model")), initial)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = plans.Submit("actual-team", teamplan.CurrentIdentity(plan), nil)
	if err != nil {
		t.Fatal(err)
	}
	action := plan.Clone()
	action.Set("expectedRevision", plan.Get("revision"))
	action.Set("requestId", raw("real-human-approval"))
	post("/api/teams/actual-team/plan/approve", action)
	// Approval starts the actual saved leader even before the UI connects.
	ws, err = websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+parent, "", host.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	ws.SetDeadline(time.Now().Add(30 * time.Second))
	pending := receive("permission_request")
	if pending.String("agentId") != "one@actual-team" {
		t.Fatal("parent pending wrong actual identity", string(raw(pending)))
	}
	updated := obj(pending.Get("input"))
	updated.Set("answers", raw(map[string]string{"Approve real team?": "Go"}))
	send(map[string]any{"type": "permission_response", "requestId": pending.String("requestId"), "allowed": true, "updatedInput": updated})
	receive("permission_resolved")
	await := func(check func() bool) {
		t.Helper()
		for !check() {
			if err := context.Cause(ctx); err != nil {
				mu.Lock()
				t.Errorf("actual requests %#v", rounds)
				mu.Unlock()
				t.Fatal(err)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	await(func() bool {
		one, _ := os.ReadFile(filepath.Join(workspace, "team-one.txt"))
		two, _ := os.ReadFile(filepath.Join(workspace, "team-two.txt"))
		v, _ := plans.Read("actual-team")
		return string(one) == "ACTUAL_TEAM_ONE_GO" && string(two) == "ACTUAL_TEAM_TWO" && v != nil && v.String("state") == "running" && len(rowsForTeam(mailbox.ReadHistory("team-lead", "actual-team"))) >= 2
	})
	if err = mailbox.Write("one", "actual-team", protocol.NewJSONObject(field("from", "two"), field("timestamp", time.Now().UTC().Format(time.RFC3339Nano)), field("text", "wake real same child"))); err != nil {
		t.Fatal(err)
	}
	await(func() bool {
		mu.Lock()
		count := rounds["one"]
		mu.Unlock()
		return count >= 7 && len(rowsForTeam(mailbox.ReadHistory("team-lead", "actual-team"))) >= 3
	})
	unread, err := mailbox.Unread("one", "actual-team")
	if err != nil || len(unread) != 0 {
		t.Fatal("accepted inbox not marked", unread, err)
	}
	// Actual desktop process approvals are intentionally excluded from the pane
	// protocol. A worker's own SDK/owner close must retire its server registry,
	// letting the real supervisor publish terminated state before TeamDelete.
	for _, name := range []string{"one", "two"} {
		request := protocol.NewJSONObject(field("type", "shutdown_request"), field("requestId", "shutdown-123@"+name), field("from", "team-lead"), field("timestamp", time.Now().UTC().Format(time.RFC3339Nano)))
		if err := mailbox.Write(name, "actual-team", protocol.NewJSONObject(field("from", "team-lead"), field("timestamp", time.Now().UTC().Format(time.RFC3339Nano)), field("text", string(raw(request))))); err != nil {
			t.Fatal(err)
		}
	}
	await(func() bool {
		current := config.Read("actual-team")
		if current == nil {
			return false
		}
		for _, member := range rowsForTeam(current.Get("members")) {
			if member.String("backendType") == "process" && (string(member.Get("isActive")) != "false" || string(member.Get("terminated")) != "true" || s.TeamWorkerConnected(member.String("sessionId"))) {
				return false
			}
		}
		return true
	})
	send(map[string]any{"type": "user_message", "content": "CLEANUP_ACTUAL_TEAM"})
	await(func() bool { return config.Read("actual-team") == nil })
	receive("message_complete")
	requestDesktopSessionSync(t, ws)
	assertDesktopIdleSnapshot(t, receive("session_state"))
	if err = s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if config.Read("actual-team") != nil {
		t.Fatal("actual TeamDelete retained team directories")
	}
	if s.TeamRuntimeRunning(plan.String("planId")) {
		t.Fatal("closed server retained launch ownership")
	}
}
func rowsForTeam(data json.RawMessage) []protocol.JSONObject {
	var rows []protocol.JSONObject
	_ = json.Unmarshal(data, &rows)
	return rows
}
