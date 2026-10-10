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
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
	"golang.org/x/net/websocket"
)

func TestActualDesktopTeamWorkerPrepareBarrierParentQuestionAndStop(t *testing.T) {
	var calls atomic.Int32
	var lastRequest atomic.Value
	question := map[string]any{"questions": []any{map[string]any{"question": "Continue worker?", "header": "Worker", "options": []any{map[string]string{"label": "Go", "description": "Proceed"}, map[string]string{"label": "Stop", "description": "Stop"}}, "multiSelect": false}}}
	var workspace string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch round := calls.Add(1); round {
		case 1:
			response(w, "parent-ready", "", nil, "parent ready")
		case 2, 5:
			response(w, "worker-question", "AskUserQuestion", question, "")
		case 3:
			body, _ := io.ReadAll(r.Body)
			if !bytes.Contains(body, []byte("Go")) || !bytes.Contains(body, []byte("WORKER_APPROVED_PROMPT")) || !bytes.Contains(body, []byte("<command-name>/model")) {
				t.Errorf("child actual next model request %s", body)
			}
			response(w, "worker-write", "Write", map[string]any{"file_path": filepath.Join(workspace, "team-ui-proof.txt"), "content": "TEAM_PARENT_QUESTION_GO"}, "")
		case 4:
			body, _ := io.ReadAll(r.Body)
			lastRequest.Store(string(body))
			response(w, "worker-complete", "", nil, "worker done")
		default:
			t.Errorf("unexpected real child POST %d", round)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	workspace = o.WorkDir
	o.Arguments.Tools = []string{"Read,Write,AskUserQuestion,TaskCreate,TaskGet,TaskList,TaskUpdate"}
	o.Environment["CLAUDE_CODE_ENABLE_TASKS"] = "1"
	o.Environment["DISABLE_TELEMETRY"] = "1"
	o.Environment["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "true"
	s, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: filepath.Join(o.Environment["HOME"], "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	providerResponse, e := host.Client().Post(host.URL+"/api/providers", "application/json", bytes.NewReader(raw(map[string]any{"presetId": "deepseek", "name": "Private team fixture", "apiKey": "fake-team-saved-key", "baseUrl": provider.URL, "apiFormat": "anthropic", "models": map[string]string{"main": "fixture-model", "haiku": "", "sonnet": "", "opus": ""}})))
	if e != nil {
		t.Fatal(e)
	}
	providerBody, _ := io.ReadAll(providerResponse.Body)
	providerResponse.Body.Close()
	if providerResponse.StatusCode != 201 {
		t.Fatal(providerResponse.StatusCode, string(providerBody))
	}
	providerID := obj(obj(providerBody).Get("provider")).String("id")
	res, err := host.Client().Post(host.URL+"/api/sessions", "application/json", bytes.NewReader(raw(map[string]any{"workDir": o.WorkDir, "permissionMode": "bypassPermissions"})))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 201 {
		t.Fatal(res.StatusCode, string(data))
	}
	parentID := obj(data).String("sessionId")
	ws, err := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+parentID, "", host.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	ws.SetDeadline(time.Now().Add(25 * time.Second))
	send := func(value any) {
		t.Helper()
		if err := websocket.Message.Send(ws, string(raw(value))); err != nil {
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
			frame := obj([]byte(data))
			if frame.String("type") == "error" {
				t.Fatal(data)
			}
			if frame.String("type") == want {
				return frame
			}
		}
	}
	send(map[string]any{"type": "user_message", "content": "initialize the actual leader"})
	receive("message_complete")
	for receive("session_state").String("turnState") != "idle" {
	}
	names := []string{"Read", "Write", "AskUserQuestion", "TaskCreate", "TaskGet", "TaskList", "TaskUpdate"}
	prepare := func(name string) string {
		t.Helper()
		id, err := s.PrepareTeamWorker(context.Background(), server.TeamWorkerLaunch{ProviderID: &providerID, WorkDir: o.WorkDir, Model: "fixture-model", PermissionMode: "bypassPermissions", Worker: teamworker.Start{ParentSessionID: parentID, TeamName: "team-private", MemberID: name, Name: name, SystemPrompt: "WORKER_APPROVED_PROMPT", Tools: &names, AgentDefinition: protocol.NewJSONObject()}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	child := prepare("first")
	if calls.Load() != 1 || !s.TeamWorkerConnected(child) {
		t.Fatal("preparation executed a model or did not acknowledge actual Host", calls.Load())
	}
	if err = s.ReleaseTeamWorker(context.Background(), child, "perform the approved child task", func() bool { return false }); err == nil {
		t.Fatal("revoked release admitted")
	}
	if err = s.ReleaseTeamWorker(context.Background(), child, "perform the approved child task", nil); err != nil {
		t.Fatal(err)
	}
	pending := receive("permission_request")
	if pending.String("agentId") != "first@team-private" || pending.String("displayName") != "first" {
		t.Fatal("parent permission identity", string(raw(pending)))
	}
	id := pending.String("requestId")
	send(map[string]any{"type": "sync_state"})
	for {
		snapshot := receive("permission_requests_snapshot")
		var ids []string
		_ = json.Unmarshal(snapshot.Get("toolRequestIds"), &ids)
		if len(ids) == 1 && ids[0] == id {
			break
		}
	}
	updated := obj(pending.Get("input"))
	updated.Set("answers", raw(map[string]string{"Continue worker?": "Go"}))
	send(map[string]any{"type": "permission_response", "requestId": id, "allowed": true, "updatedInput": updated})
	resolved := receive("permission_resolved")
	if resolved.String("requestId") != id {
		t.Fatal(string(raw(resolved)))
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		proof, _ := os.ReadFile(filepath.Join(workspace, "team-ui-proof.txt"))
		if string(proof) == "TEAM_PARENT_QUESTION_GO" && calls.Load() == 4 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	proof, _ := os.ReadFile(filepath.Join(workspace, "team-ui-proof.txt"))
	if string(proof) != "TEAM_PARENT_QUESTION_GO" || calls.Load() != 4 {
		t.Fatal("same child permission Future did not continue", string(proof), calls.Load(), lastRequest.Load())
	}
	second := prepare("second")
	if err = s.ReleaseTeamWorker(context.Background(), second, "wait for permission then be stopped", nil); err != nil {
		t.Fatal(err)
	}
	receive("permission_request")
	send(map[string]any{"type": "stop_generation"})
	receive("permission_resolved")
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && (s.TeamWorkerConnected(child) || s.TeamWorkerConnected(second)) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.TeamWorkerConnected(child) || s.TeamWorkerConnected(second) {
		t.Fatal("parent stop retained actual child Owners")
	}
}
