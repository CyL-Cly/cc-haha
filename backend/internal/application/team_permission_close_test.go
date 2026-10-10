package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualParentCloseRejectsIdleIndependentPermissionBeforeJoinAndConcurrentCloseWaits(t *testing.T) {
	var parent, child atomic.Int32
	target := ""
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte("# Agent Teammate Communication")) {
			if child.Add(1) != 1 {
				t.Error("cancelled permission generated model request")
			}
			response(w, "pending-child-write", "Write", map[string]any{"file_path": target, "content": "must-not-write"}, "")
			return
		}
		switch parent.Add(1) {
		case 1:
			response(w, "close-create", "TeamCreate", map[string]any{"team_name": "actual-pending-close"}, "")
		case 2:
			response(w, "close-spawn", "Agent", map[string]any{"name": "worker", "prompt": "write only if human approves", "description": "actual pending child"}, "")
		case 3:
			response(w, "close-idle", "", nil, "actual parent idle")
		default:
			t.Error("close queried model")
			http.Error(w, "unexpected", 500)
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Arguments.PermissionMode = "default"
	o.Arguments.Tools = []string{"TeamCreate,TeamDelete,Agent,Read,Write,SendMessage"}
	o.Native.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
	o.OnSDK = func(json.RawMessage) error { return nil }
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if e := os.MkdirAll(config, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"enabledPlugins":{"sample@mkt":false},"agentTeamsEnabled":false}`), 0600); e != nil {
		t.Fatal(e)
	}
	target = filepath.Join(o.WorkDir, "never-approved.txt")
	s, e := NewSession(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// SDK/headless provider output is observed normally, while no permission
	// consumer answers this genuine child's ask. Close must release it.
	if e = s.Submit(ctx, tui.Input{Text: "create a team and leave an actual pending child"}, nil); e != nil {
		t.Fatal(e)
	}
	for len(s.PendingPermissions()) == 0 {
		select {
		case <-ctx.Done():
			t.Fatal("real parent permission never pending")
		case <-time.After(10 * time.Millisecond):
		}
	}
	var wg sync.WaitGroup
	closeResults := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); closeResults <- s.Close(ctx) }()
	}
	wg.Wait()
	close(closeResults)
	for e := range closeResults {
		if e != nil {
			t.Fatal("pending permission/worker close failed", e)
		}
	}
	if len(s.PendingPermissions()) != 0 || parent.Load() != 3 || child.Load() != 1 {
		t.Fatal("actual close lifecycle mismatch", len(s.PendingPermissions()), parent.Load(), child.Load())
	}
	if _, e = os.Stat(target); !os.IsNotExist(e) {
		t.Fatal("close approved a pending write", e)
	}
	if e := s.process.Owner.Run(context.Background(), func(*cooperative.Scope) error { return nil }); !errors.Is(e, cooperative.ErrClosed) {
		t.Fatal("close returned before actual Owner join")
	}
}
