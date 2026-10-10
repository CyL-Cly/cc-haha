package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestChildAgentPermissionRetainsOriginalIdentityAcrossDesktopSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var calls atomic.Int32
	var original string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			response(w, "parent-permission", "Agent", map[string]any{"prompt": "Create the approved child file", "description": "permission child", "subagent_type": "general-purpose"}, "")
		case 2:
			response(w, "child-permission", "Write", map[string]any{"file_path": original, "content": "unapproved"}, "")
		case 3:
			response(w, "child-complete", "", nil, "APPROVED CHILD")
		case 4:
			response(w, "parent-complete", "", nil, "APPROVED PARENT")
		default:
			t.Error("unexpected model call")
			http.Error(w, "unexpected", 400)
		}
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	o.Arguments.PermissionMode = "default"
	o.Arguments.Tools = []string{"Read,Write,Edit,Bash,Glob,Grep,Agent"}
	original = filepath.Join(o.WorkDir, "child-original.txt")
	approved := filepath.Join(o.WorkDir, "child-approved.txt")
	var mu sync.Mutex
	frames := []json.RawMessage{}
	permissions := make(chan json.RawMessage, 1)
	live, err := DesktopFactory(o)(ctx, server.SessionOptions{ID: o.Arguments.SessionID, WorkDir: o.WorkDir, PermissionMode: "default"}, func(value json.RawMessage) {
		mu.Lock()
		frames = append(frames, append(json.RawMessage(nil), value...))
		mu.Unlock()
		if obj(value).String("type") == "permission_request" {
			permissions <- value
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	d := live.(*DesktopSession)
	defer d.Close(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- d.engine.Submit(ctx, tui.Input{Mode: "prompt", Text: "Run a child with an actual approval"}, d.events)
	}()
	var request json.RawMessage
	select {
	case request = <-permissions:
	case err := <-done:
		t.Fatalf("child approval missing: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	permission := obj(request)
	agentID := permission.String("agentId")
	if !strings.HasPrefix(agentID, "a") || len(agentID) != 17 || permission.String("toolName") != "Write" || permission.String("toolUseId") != "call-child-permission" {
		t.Fatalf("child permission identity: %s", request)
	}
	if calls.Load() != 2 {
		t.Fatalf("model continued before approval: %d", calls.Load())
	}
	if _, err = os.Stat(original); !os.IsNotExist(err) {
		t.Fatal("pending child write executed", err)
	}
	snapshot, err := d.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var replay json.RawMessage
	for _, frame := range snapshot {
		if obj(frame).String("type") == "permission_request" {
			replay = frame
		}
	}
	if obj(replay).String("agentId") != agentID || obj(replay).String("toolUseId") != permission.String("toolUseId") || obj(replay).String("requestId") != permission.String("requestId") {
		t.Fatalf("snapshot lost actual child route: %s", raw(snapshot))
	}
	if err = d.engine.RespondPermission(ctx, permission.String("requestId"), tui.PermissionDecision{Allowed: true, UpdatedInput: raw(map[string]any{"file_path": approved, "content": "approved child"})}); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(approved); err != nil || string(data) != "approved child" {
		t.Fatalf("approved child input did not reach actual tool: %s %v", data, err)
	}
	if _, err = os.Stat(original); !os.IsNotExist(err) {
		t.Fatal("original child input executed", err)
	}
	mu.Lock()
	defer mu.Unlock()
	completed := false
	for _, frame := range frames {
		m := obj(frame)
		if m.String("type") == "agent_run_event" && m.String("targetAgentId") == agentID && obj(m.Get("event")).String("state") == "idle" {
			completed = true
		}
	}
	if !completed || calls.Load() != 4 {
		t.Fatalf("child stream/finalizer did not complete: calls=%d frames=%s", calls.Load(), raw(frames))
	}
}
