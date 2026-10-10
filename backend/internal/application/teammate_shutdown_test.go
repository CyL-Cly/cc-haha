package application

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualMainWorkerShutdownToolReplyPersistsProjectsThenOnlyOwnSessionClosesAndJoins(t *testing.T) {
	var posts atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if posts.Add(1) != 1 {
			t.Error("shutdown made another model POST")
		}
		response(w, "actual-main-shutdown", "SendMessage", map[string]any{"to": "team-lead", "message": map[string]any{"type": "shutdown_response", "request_id": "actual-main-request", "approve": true}}, "")
	}))
	defer peer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	parentOptions := nativeSessionOptions(t, peer.URL)
	parent, err := NewSession(ctx, parentOptions)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close(context.Background())
	o := nativeSessionOptions(t, peer.URL)
	o.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
	o.Native.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
	if err = os.MkdirAll(o.Environment["CLAUDE_CONFIG_DIR"], 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"agentTeamsEnabled":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	tools := []string{"SendMessage"}
	o.TeamWorker = &teamworker.Start{ParentSessionID: parent.process.Controller.SessionID(), TeamName: "actual-main-close", MemberID: "actual-member", Name: "worker", SystemPrompt: "actual shutdown MAIN worker", Tools: &tools, AgentDefinition: protocol.NewJSONObject()}
	store, err := taskstore.New(taskstore.Options{ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], PID: os.Getpid(), Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	config, err := teamstore.NewConfig(teamstore.ConfigOptions{ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], TaskStore: store, PID: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	if err = config.Write("actual-main-close", obj(raw(map[string]any{"name": "actual-main-close", "createdAt": 1700000000000, "members": []map[string]any{{"name": "worker", "agentId": "worker@actual-main-close", "tmuxPaneId": "actual-pane", "backendType": "tmux"}}}))); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	frames := []json.RawMessage{}
	o.OnSDK = func(data json.RawMessage) error {
		mu.Lock()
		frames = append(frames, append(json.RawMessage(nil), data...))
		mu.Unlock()
		return nil
	}
	worker, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close(context.Background())
	path := ""
	if err = worker.process.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		path, err = worker.process.Controller.Writer().ComputedTranscriptPath(scope)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	err = worker.SubmitSDK(ctx, obj(raw(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "Respond to actual-main-request shutdown approval"}})))
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal("actual turn failed", err)
	}
	worker.mu.Lock()
	done := worker.teammateShutdownDone
	worker.mu.Unlock()
	if done == nil {
		t.Fatal("actual shutdown callback never called")
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("actual worker shutdown did not join")
	}
	worker.mu.Lock()
	failure, closed := worker.teammateShutdownError, worker.closed
	worker.mu.Unlock()
	if failure != nil || !closed {
		t.Fatal("actual closure", failure, closed)
	}
	if posts.Load() != 1 {
		t.Fatal("extra model after approved shutdown", posts.Load())
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "Shutdown approved. Sent confirmation") {
		t.Fatal("tool response missing from actual transcript", err)
	}
	mu.Lock()
	reply := false
	for _, data := range frames {
		if strings.Contains(string(data), "Shutdown approved. Sent confirmation") {
			reply = true
		}
	}
	mu.Unlock()
	if !reply {
		t.Fatal("actual SDK response was not projected before closing")
	}
	if err = parent.process.Owner.Run(ctx, func(scope *cooperative.Scope) error { _, err := parent.process.Host.InferenceModel(scope); return err }); err != nil {
		t.Fatal("worker shutdown closed leader owner", err)
	}
}
