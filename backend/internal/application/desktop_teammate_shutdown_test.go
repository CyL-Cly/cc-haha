package application

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestDesktopTeammateExitClosesActualSDKTransportAndOldEngineCannotCloseReplacement(t *testing.T) {
	var posts atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		response(w, "actual-desktop-shutdown", "SendMessage", map[string]any{"to": "team-lead", "message": map[string]any{"type": "shutdown_response", "request_id": "actual-desktop-request", "approve": true}}, "")
	}))
	defer peer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	o := nativeSessionOptions(t, peer.URL)
	o.Native.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
	o.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
	if err := os.MkdirAll(o.Environment["CLAUDE_CONFIG_DIR"], 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"agentTeamsEnabled":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	tools := []string{"SendMessage"}
	start := &teamworker.Start{ParentSessionID: "parent", TeamName: "actual-desktop-exit", MemberID: "m", Name: "worker", SystemPrompt: "actual desktop worker", Tools: &tools, AgentDefinition: protocol.NewJSONObject()}
	live, err := DesktopFactory(o)(ctx, server.SessionOptions{ID: "10ff6990-8815-497a-8998-cd942c8a497d", WorkDir: o.WorkDir, TeamWorker: start, PermissionMode: "bypassPermissions"}, func(json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	d := live.(*DesktopSession)
	defer d.Close(context.Background())
	d.BindSDKResponseTransport(func(json.RawMessage) error { return nil })
	state := d.sdkRuntime()
	input := obj(raw(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "Approve actual-desktop-request shutdown"}}))
	if err = d.SubmitTeamTask(ctx, input); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for ctx.Err() == nil {
		d.mu.Lock()
		closed := d.closed
		d.mu.Unlock()
		state.mu.Lock()
		sdkClosed := state.closed
		state.mu.Unlock()
		if closed && sdkClosed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatal("desktop worker did not close actual SDK transport")
	}
	if posts.Load() != 1 {
		t.Fatal("extra desktop model after shutdown", posts.Load())
	}
	// A delayed request from an old real engine closes only that old engine.
	oldOptions := nativeSessionOptions(t, peer.URL)
	old, err := NewSession(ctx, oldOptions)
	if err != nil {
		t.Fatal(err)
	}
	newOptions := nativeSessionOptions(t, peer.URL)
	replacement, err := NewSession(ctx, newOptions)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close(context.Background())
	wrapper := &DesktopSession{engine: replacement, ctx: ctx, emit: func(json.RawMessage) {}, projection: NewProjection()}
	if err = wrapper.closeDesktopEngine(ctx, old); err != nil {
		t.Fatal(err)
	}
	wrapper.mu.Lock()
	closed := wrapper.closed
	wrapper.mu.Unlock()
	if closed {
		t.Fatal("old worker closed replacement desktop")
	}
	if err = replacement.process.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		_, err := replacement.process.Host.InferenceModel(scope)
		return err
	}); err != nil {
		t.Fatal("old worker closed replacement owner", err)
	}
}
