package application

import (
	"bytes"
	"context"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
	"golang.org/x/net/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A real child can request permission before the leader finishes a concurrent
// turn. The UI clears turn-local permissions at message_complete, so the
// original server replays still-pending teammate requests immediately afterward.
func TestActualTeamPermissionReplayedAfterLeadCompletionThenRoutesAndExecutes(t *testing.T) {
	var parentCalls, childCalls atomic.Int32
	var lastBody atomic.Value
	t.Cleanup(func() {
		if t.Failed() {
			body, _ := lastBody.Load().(string)
			t.Logf("actual requests parent%d child%d last%s", parentCalls.Load(), childCalls.Load(), body)
		}
	})
	var workspace string
	childCompleted := make(chan struct{}, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		lastBody.Store(string(body))
		if !bytes.Contains(body, []byte("perform actual paired tools")) {
			parentCalls.Add(1)
			response(w, "lead-response", "", nil, "lead done while child remains pending")
			return
		}
		if childCalls.Add(1) == 1 {
			w.Header().Set("content-type", "text/event-stream")
			command := "printf TEAM_PERMISSION_ACTUAL_OK > '" + filepath.Join(workspace, "team-permission-actual.txt") + "'"
			for _, event := range []struct {
				name  string
				value any
			}{
				{"message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "actual-bash-task-pair", "type": "message", "role": "assistant", "model": "fixture-model", "content": []any{}, "usage": map[string]int{"input_tokens": 1, "output_tokens": 0}}}},
				{"content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": "actual-child-bash", "name": "Bash", "input": map[string]any{}}}},
				{"content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(raw(map[string]string{"command": command}))}}},
				{"content_block_stop", map[string]any{"type": "content_block_stop", "index": 0}},
				{"content_block_start", map[string]any{"type": "content_block_start", "index": 1, "content_block": map[string]any{"type": "tool_use", "id": "actual-child-taskget", "name": "TaskGet", "input": map[string]any{}}}},
				{"content_block_delta", map[string]any{"type": "content_block_delta", "index": 1, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(raw(map[string]string{"taskId": "missing-task"}))}}},
				{"content_block_stop", map[string]any{"type": "content_block_stop", "index": 1}},
				{"message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "tool_use"}, "usage": map[string]int{"output_tokens": 2}}},
				{"message_stop", map[string]any{"type": "message_stop"}},
			} {
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.name, raw(event.value))
			}
			return
		}
		if !bytes.Contains(body, []byte("actual-child-bash")) || !bytes.Contains(body, []byte("actual-child-taskget")) {
			t.Error("actual child lost paired tool results", string(body))
		}
		response(w, "child-completed", "", nil, "child tools completed after approval")
		childCompleted <- struct{}{}
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	workspace = o.WorkDir
	o.Arguments.Tools = []string{"Bash,TaskGet"}
	o.Environment["CLAUDE_CODE_ENABLE_TASKS"] = "1"
	s, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return newIsolatedProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: filepath.Join(o.Environment["HOME"], "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	providerResponse, err := host.Client().Post(host.URL+"/api/providers", "application/json", bytes.NewReader(raw(map[string]any{"presetId": "deepseek", "name": "Private real permission fixture", "apiKey": "fake-private-team-key", "baseUrl": provider.URL, "apiFormat": "anthropic", "models": map[string]string{"main": "fixture-model", "haiku": "", "sonnet": "", "opus": ""}})))
	if err != nil {
		t.Fatal(err)
	}
	providerBody, _ := io.ReadAll(providerResponse.Body)
	providerResponse.Body.Close()
	if providerResponse.StatusCode != 201 {
		t.Fatal(providerResponse.StatusCode, string(providerBody))
	}
	providerID := obj(obj(providerBody).Get("provider")).String("id")
	res, err := host.Client().Post(host.URL+"/api/sessions", "application/json", bytes.NewReader(raw(map[string]any{"workDir": o.WorkDir, "permissionMode": "default"})))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 201 {
		t.Fatal(res.StatusCode, string(body))
	}
	parentID := obj(body).String("sessionId")
	ws, err := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+parentID, "", host.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = ws.SetDeadline(time.Now().Add(15 * time.Second))
	send := func(value any) {
		t.Helper()
		if err := websocket.Message.Send(ws, string(raw(value))); err != nil {
			t.Fatal(err)
		}
	}
	receive := func() protocol.JSONObject {
		t.Helper()
		var data string
		if err := websocket.Message.Receive(ws, &data); err != nil {
			t.Fatal(err)
		}
		frame := obj([]byte(data))
		if frame.String("type") == "error" {
			t.Fatal(data)
		}
		return frame
	}
	send(map[string]any{"type": "user_message", "content": "initialize actual lead"})
	for receive().String("type") != "message_complete" {
	}
	send(map[string]any{"type": "sync_state"})
	for {
		frame := receive()
		if frame.String("type") == "session_state" && frame.String("turnState") == "idle" {
			break
		}
	}
	names := []string{"Bash", "TaskGet"}
	child, err := s.PrepareTeamWorker(context.Background(), server.TeamWorkerLaunch{ProviderID: &providerID, WorkDir: workspace, Model: "fixture-model", PermissionMode: "default", Worker: teamworker.Start{ParentSessionID: parentID, TeamName: "actual-pending-team", MemberID: "actual-pending-child", Name: "alpha", SystemPrompt: "PRIVATE_PENDING_BASH_WORKER", Tools: &names, AgentDefinition: protocol.NewJSONObject()}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ReleaseTeamWorker(context.Background(), child, "perform actual paired tools", nil); err != nil {
		t.Fatal(err)
	}
	var pending protocol.JSONObject
	for {
		frame := receive()
		if frame.String("type") == "permission_request" {
			pending = frame
			break
		}
	}
	if pending.String("toolName") != "Bash" || pending.String("agentId") != "alpha@actual-pending-team" {
		t.Fatal("wrong actual child permission", string(raw(pending)))
	}
	send(map[string]any{"type": "user_message", "content": "show lead progress while worker waits"})
	completed, replayed := false, false
	for {
		frame := receive()
		if frame.String("type") == "message_complete" {
			completed = true
		}
		if completed && frame.String("type") == "permission_request" && frame.String("requestId") == pending.String("requestId") {
			replayed = true
		}
		if completed && replayed {
			break
		}
	}
	if !replayed {
		t.Error("waiting child permission was cleared by leader completion without original Source replay")
	}
	send(map[string]any{"type": "permission_response", "requestId": pending.String("requestId"), "allowed": true})
	select {
	case <-childCompleted:
	case <-time.After(8 * time.Second):
		t.Fatal("parent permission response did not release actual child Bash/TaskGet")
	}
	proof, err := os.ReadFile(filepath.Join(workspace, "team-permission-actual.txt"))
	if err != nil || string(proof) != "TEAM_PERMISSION_ACTUAL_OK" {
		t.Fatal("actual approved Bash did not execute", string(proof), err)
	}
	if parentCalls.Load() != 2 || childCalls.Load() != 2 {
		t.Fatal("unexpected actual requests", parentCalls.Load(), childCalls.Load())
	}
	if err = s.StopTeamWorker(context.Background(), child); err != nil {
		t.Fatal(err)
	}
}
