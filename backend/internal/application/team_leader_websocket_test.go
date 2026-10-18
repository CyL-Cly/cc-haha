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
	"strings"
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

func TestActualDesktopLeaderIdleMailboxReconnectSamePendingAndServerCloseJoinsWorker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	var parentPosts, childPosts atomic.Int32
	childStarted, childStopped := make(chan struct{}), make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	target := ""
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte("APPROVED_DURABLE_WS_WORKER")) {
			if childPosts.Add(1) == 1 {
				close(childStarted)
			}
			select {
			case <-r.Context().Done():
			case <-release:
			}
			close(childStopped)
			return
		}
		switch parentPosts.Add(1) {
		case 1:
			response(w, "ws-initialize", "", nil, "DESKTOP_PARENT_READY")
		case 2:
			response(w, "ws-approved", "", nil, "DESKTOP_APPROVED_TEAM_RUNNING")
		case 3:
			if !bytes.Contains(body, []byte("NEXT_DESKTOP_HUMAN")) {
				t.Error("next desktop human missing")
			}
			response(w, "ws-human", "", nil, "DESKTOP_NEXT_HUMAN_OK")
		case 4:
			if !bytes.Contains(body, []byte("DESKTOP_PHYSICAL_PEER")) {
				t.Error("peer not in actual history")
			}
			response(w, "ws-idle-write", "Write", map[string]any{"file_path": target, "content": "DESKTOP_DURABLE_APPROVED"}, "")
		case 5:
			response(w, "ws-idle-complete", "", nil, "DESKTOP_IDLE_FOLLOW_UP_COMPLETE")
		default:
			t.Error("extra actual parent POST", parentPosts.Load())
			http.Error(w, "unexpected", 500)
		}
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	o.Arguments.Tools = []string{"Agent,TeamCreate,TeamDelete,Write,Read,SendMessage"}
	o.Arguments.PermissionMode = "default"
	o.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
	configDir := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), []byte(`{"enabledPlugins":{"sample@mkt":false},"agentTeamsEnabled":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	target = filepath.Join(o.WorkDir, "desktop-durable.txt")
	s, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: configDir, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return newIsolatedProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	post := func(path string, value any) protocol.JSONObject {
		t.Helper()
		res, e := host.Client().Post(host.URL+path, "application/json", bytes.NewReader(raw(value)))
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		want := 201
		if strings.HasSuffix(path, "/approve") {
			want = 200
		}
		if res.StatusCode != want {
			t.Fatal(res.StatusCode, string(data))
		}
		return obj(data)
	}
	providerID := obj(post("/api/providers", map[string]any{"presetId": "deepseek", "name": "private desktop team fixture", "apiKey": "fake-only-loopback", "baseUrl": provider.URL, "apiFormat": "anthropic", "models": map[string]string{"main": "fixture-model", "haiku": "fixture-model", "sonnet": "fixture-model", "opus": "fixture-model"}}).Get("provider")).String("id")
	id := post("/api/sessions", map[string]any{"workDir": o.WorkDir, "permissionMode": "default"}).String("sessionId")
	dial := func() *websocket.Conn {
		t.Helper()
		ws, e := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+id, "", host.URL)
		if e != nil {
			t.Fatal(e)
		}
		ws.SetDeadline(time.Now().Add(20 * time.Second))
		return ws
	}
	ws := dial()
	defer func() { _ = ws.Close() }()
	send := func(value any) {
		t.Helper()
		if e := websocket.Message.Send(ws, string(raw(value))); e != nil {
			t.Fatal(e)
		}
	}
	receive := func(want string) protocol.JSONObject {
		t.Helper()
		for {
			var data string
			if e := websocket.Message.Receive(ws, &data); e != nil {
				t.Fatal(e)
			}
			frame := obj([]byte(data))
			if frame.String("type") == "error" {
				t.Fatal(data)
			}
			if frame.String("type") == want {
				return frame
			}
		}
	}
	send(map[string]any{"type": "user_message", "content": "create a team and leave its actual worker active"})
	receive("message_complete")
	requestDesktopSessionSync(t, ws)
	assertDesktopIdleSnapshot(t, receive("session_state"))
	store, e := taskstore.New(taskstore.Options{ConfigDir: configDir, PID: os.Getpid(), Now: time.Now})
	if e != nil {
		t.Fatal(e)
	}
	mailbox, e := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: configDir, TaskStore: store})
	if e != nil {
		t.Fatal(e)
	}
	config, e := teamstore.NewConfig(teamstore.ConfigOptions{ConfigDir: configDir, TaskStore: store, PID: os.Getpid()})
	if e != nil {
		t.Fatal(e)
	}
	if e = config.Write("actual-durable-ws", protocol.NewJSONObject(field("name", "actual-durable-ws"), field("leadSessionId", id), field("leadAgentId", "team-lead@actual-durable-ws"), field("createdAt", 1000), field("members", []any{}))); e != nil {
		t.Fatal(e)
	}
	plans, e := teamplan.New(teamplan.Options{Config: config, Tasks: store, ConfigDir: configDir, WorkDir: o.WorkDir, Now: time.Now, UUID: func() (string, error) { return "aaaaaaaa-aaaa-4aaa-8aaa-000000000001", nil }})
	if e != nil {
		t.Fatal(e)
	}
	catalog := protocol.NewJSONObject(field("durable-worker", protocol.NewJSONObject(field("source", "built-in"), field("sourceIdentity", map[string]string{"kind": "builtin"}), field("systemPrompt", "APPROVED_DURABLE_WS_WORKER"), field("tools", []string{"Read"}))))
	initial := protocol.NewJSONObject(field("agentCatalog", catalog), field("members", []protocol.JSONObject{protocol.NewJSONObject(field("id", "worker"), field("name", "worker"), field("agentType", "durable-worker"), field("prompt", "wait while actual leader continues"), field("runtime", map[string]string{"providerId": providerID, "modelId": "fixture-model"}))}), field("tasks", []protocol.JSONObject{protocol.NewJSONObject(field("id", "real-task"), field("subject", "actual durable work"), field("ownerId", "worker"), field("dependencies", []string{}))}))
	plan, e := plans.EnsureDraft("actual-durable-ws", id, protocol.NewJSONObject(field("providerId", providerID), field("modelId", "fixture-model")), initial)
	if e != nil {
		t.Fatal(e)
	}
	plan, e = plans.Submit("actual-durable-ws", teamplan.CurrentIdentity(plan), nil)
	if e != nil {
		t.Fatal(e)
	}
	action := plan.Clone()
	action.Set("expectedRevision", plan.Get("revision"))
	action.Set("requestId", raw("actual-human-approval"))
	post("/api/teams/actual-durable-ws/plan/approve", action)
	receive("message_complete")
	requestDesktopSessionSync(t, ws)
	assertDesktopIdleSnapshot(t, receive("session_state"))
	select {
	case <-childStarted:
	case <-ctx.Done():
		t.Fatal("actual child never started")
	}
	send(map[string]any{"type": "user_message", "content": "NEXT_DESKTOP_HUMAN"})
	receive("message_complete")
	requestDesktopSessionSync(t, ws)
	assertDesktopIdleSnapshot(t, receive("session_state"))
	if e = mailbox.Write("team-lead", "actual-durable-ws", protocol.NewJSONObject(field("from", "worker"), field("text", "DESKTOP_PHYSICAL_PEER"), field("timestamp", "2026-10-07T00:00:00.000Z"))); e != nil {
		t.Fatal(e)
	}
	pending := receive("permission_request")
	request := pending.String("requestId")
	if pending.String("toolName") != "Write" || pending.String("agentId") != "" {
		t.Fatal("wrong parent permission identity", string(raw(pending)))
	}
	_ = ws.Close()
	ws = dial()
	restored := receive("permission_request")
	if restored.String("requestId") != request {
		t.Fatal("reconnect replaced pending future", string(raw(restored)))
	}
	snapshot := receive("permission_requests_snapshot")
	var ids []string
	_ = json.Unmarshal(snapshot.Get("toolRequestIds"), &ids)
	if len(ids) != 1 || ids[0] != request || string(snapshot.Get("turnActive")) != "false" {
		t.Fatal("reconnect snapshot incorrect", string(raw(snapshot)))
	}
	requestDesktopSessionSync(t, ws)
	state := receive("session_state")
	if state.String("turnState") != "idle" {
		t.Fatal("internal SDK run incorrectly registered a foreground user turn", string(raw(state)))
	}
	send(map[string]any{"type": "permission_response", "requestId": request, "allowed": true})
	receive("permission_resolved")
	receive("message_complete")
	if data, e := os.ReadFile(target); e != nil || string(data) != "DESKTOP_DURABLE_APPROVED" {
		t.Fatal("real approved artifact missing", e, string(data))
	}
	if e = s.Close(ctx); e != nil {
		t.Fatal("actual server/worker did not join", e)
	}
	select {
	case <-childStopped:
	case <-ctx.Done():
		t.Fatal("physical child HTTP survived close")
	}
	if childPosts.Load() != 1 || parentPosts.Load() != 5 {
		t.Fatal("unexpected genuine model lifecycle", childPosts.Load(), parentPosts.Load())
	}
}
