package application

import (
	"bytes"
	"context"
	"io"
	"io/fs"
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

func TestActualDesktopProcessTeamShutdownRetiresOwnedServerRuntimeAndColdResumesSameSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var posts atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch posts.Add(1) {
		case 1:
			response(w, "actual-parent", "", nil, "parent ready")
		case 2:
			response(w, "actual-worker-close", "SendMessage", map[string]any{"to": "team-lead", "message": map[string]any{"type": "shutdown_response", "request_id": "actual-owned-shutdown", "approve": true}}, "")
		default:
			t.Error("unexpected actual model request after shutdown")
			http.Error(w, "unexpected model request", 500)
		}
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
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
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: filepath.Join(o.Environment["HOME"], "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	post := func(path string, value any) protocol.JSONObject {
		t.Helper()
		res, err := host.Client().Post(host.URL+path, "application/json", bytes.NewReader(raw(value)))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		if res.StatusCode != 201 {
			t.Fatal(res.StatusCode, string(data))
		}
		return obj(data)
	}
	providerID := obj(post("/api/providers", map[string]any{"presetId": "deepseek", "name": "Private retirement fixture", "apiKey": "fake-private-key", "baseUrl": provider.URL, "apiFormat": "anthropic", "models": map[string]string{"main": "fixture-model", "haiku": "", "sonnet": "", "opus": ""}}).Get("provider")).String("id")
	parentID := post("/api/sessions", map[string]any{"workDir": o.WorkDir, "permissionMode": "bypassPermissions"}).String("sessionId")
	ws, err := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+parentID, "", host.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = ws.SetDeadline(time.Now().Add(15 * time.Second))
	if err := websocket.Message.Send(ws, string(raw(map[string]any{"type": "user_message", "content": "initialize actual parent"}))); err != nil {
		t.Fatal(err)
	}
	parentCompleted := false
	for {
		var data string
		if err := websocket.Message.Receive(ws, &data); err != nil {
			t.Fatal(err)
		}
		frame := obj([]byte(data))
		if frame.String("type") == "error" {
			t.Fatal(data)
		}
		if !parentCompleted && frame.String("type") == "message_complete" {
			parentCompleted = true
			requestDesktopSessionSync(t, ws)
		}
		if parentCompleted && frame.String("type") == "session_state" {
			assertDesktopIdleSnapshot(t, frame)
			break
		}
	}
	const childID = "92587d46-bc12-497a-9055-914cf3a56ebd"
	teamDir := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "teams", "actual-process-retirement")
	if err := os.MkdirAll(teamDir, 0700); err != nil {
		t.Fatal(err)
	}
	config := map[string]any{"name": "actual-process-retirement", "createdAt": 1000, "leadSessionId": parentID, "leadAgentId": "team-lead@actual-process-retirement", "members": []any{map[string]any{"agentId": "team-lead@actual-process-retirement", "name": "team-lead"}, map[string]any{"agentId": "worker@actual-process-retirement", "name": "worker", "backendType": "process", "tmuxPaneId": "", "sessionId": childID, "isActive": true, "terminated": false}}}
	if err := os.WriteFile(filepath.Join(teamDir, "config.json"), raw(config), 0600); err != nil {
		t.Fatal(err)
	}
	tools := []string{"SendMessage"}
	launch := server.TeamWorkerLaunch{ID: childID, ProviderID: &providerID, WorkDir: o.WorkDir, Model: "fixture-model", PermissionMode: "bypassPermissions", Worker: teamworker.Start{ParentSessionID: parentID, TeamName: "actual-process-retirement", MemberID: "worker", Name: "worker", SystemPrompt: "Approve actual-owned-shutdown and exit", Tools: &tools, AgentDefinition: protocol.NewJSONObject()}}
	if _, err = s.PrepareTeamWorker(ctx, launch); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseTeamWorker(ctx, childID, "Approve actual-owned-shutdown then exit", nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && s.TeamWorkerConnected(childID) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.TeamWorkerConnected(childID) {
		t.Fatal("actual process shutdown approved and engine closed, but server retained its live runtime")
	}
	if posts.Load() != 2 || !s.NativeSessionConnected(parentID) {
		t.Fatal("worker retirement ran another model or retired its parent", posts.Load())
	}
	// Original message-to-stopped-member restarts its same saved conversation.
	launch.Resume = true
	if _, err := s.PrepareTeamWorker(ctx, launch); err != nil {
		t.Fatal("self retirement retained the registry slot and blocked cold resume", err)
	}
	if !s.TeamWorkerConnected(childID) || posts.Load() != 2 {
		t.Fatal("same-session cold restore did not construct a live native worker")
	}
	if err := s.StopTeamWorker(ctx, childID); err != nil {
		t.Fatal(err)
	}
	if err := s.StopTeamWorker(ctx, childID); err != nil {
		t.Fatal("repeated actual worker stop did not remain idempotent", err)
	}
	// Original process recovery requests resume, but starts a fresh same-ID
	// worker when the actual saved transcript is absent. Remove only this
	// fixture's child transcript after both runtimes have physically joined.
	removed := 0
	projects := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects")
	if err := filepath.WalkDir(projects, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() == childID+".jsonl" {
			removed++
			return os.Remove(path)
		}
		return nil
	}); err != nil || removed == 0 {
		t.Fatal("actual saved child transcript was not materialized", removed, err)
	}
	if _, err := s.PrepareTeamWorker(ctx, launch); err != nil {
		t.Fatal("missing transcript should start a fresh same-ID worker", err)
	}
	if !s.TeamWorkerConnected(childID) || posts.Load() != 2 {
		t.Fatal("missing-transcript preparation should not issue a model request", posts.Load())
	}
	if err := s.StopTeamWorker(ctx, childID); err != nil {
		t.Fatal(err)
	}
}
