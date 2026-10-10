package tui

import (
	"context"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func teammateTestTasks() protocol.JSONObject {
	var tasks protocol.JSONObject
	_ = tasks.UnmarshalJSON([]byte(`{"actual-task":{"id":"actual-task","type":"in_process_teammate","status":"running","description":"physical work","identity":{"agentName":"worker"},"messages":[{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"ACTUAL_TEAMMATE_TRANSCRIPT"},{"type":"tool_use","id":"actual-tool","name":"Read","input":{"file_path":"physical.txt"}}]}}]}}`))
	return tasks
}

func TestTeammateTUISelectionViewInputAndEscapeKeepOriginalLeaderDraftAndPermission(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.SetTeammateTasks(teammateTestTasks())
	s.Editor.Set("original leader draft")
	for _, key := range []Key{{Name: "shift+down"}, {Name: "shift+down"}, {Name: "enter"}} {
		s.HandleKey(key, time.Now())
	}
	if s.Teammates.ViewingAgentTaskID != "actual-task" || !strings.Contains(s.View("", false).Content, "ACTUAL_TEAMMATE_TRANSCRIPT") || !strings.Contains(s.View("", false).Content, "Read: physical.txt") {
		t.Fatal("actual task view missing")
	}
	s.Editor.Set("send exact user input")
	actions := s.HandleKey(Key{Name: "enter"}, time.Now())
	if len(actions) != 1 || actions[0].kind != actionTeammate || actions[0].teammate.Kind != "send" || actions[0].teammate.TaskID != "actual-task" || actions[0].teammate.Message != "send exact user input" || s.Busy() || len(s.Entries) != 0 {
		t.Fatal("teammate input became leader model turn", actions)
	}
	actions = s.HandleKey(Key{Name: "esc"}, time.Now())
	if len(actions) != 1 || actions[0].teammate.Kind != "interrupt" || s.Teammates.ViewingAgentTaskID != "actual-task" {
		t.Fatal("Escape killed/exited running worker", actions)
	}
	s.Permissions = []PermissionRequest{{ID: "real-parent", ToolName: "Write"}}
	actions = s.HandleKey(Key{Name: "esc"}, time.Now())
	if len(actions) != 1 || actions[0].kind != actionPermission || actions[0].permission != "real-parent" || actions[0].decision.Allowed {
		t.Fatal("teammate view swallowed actual parent permission", actions)
	}
}

type teammateTerminalSession struct {
	*testSession
	mu       sync.Mutex
	callback func(protocol.JSONObject)
	ready    chan struct{}
	actions  chan TeammateAction
	removed  atomic.Bool
}

func (s *teammateTerminalSession) SubscribeTeammates(_ context.Context, callback func(protocol.JSONObject)) (protocol.JSONObject, func(), error) {
	s.mu.Lock()
	s.callback = callback
	s.mu.Unlock()
	close(s.ready)
	return teammateTestTasks(), func() { s.mu.Lock(); s.callback = nil; s.mu.Unlock(); s.removed.Store(true) }, nil
}
func (s *teammateTerminalSession) ControlTeammate(_ context.Context, action TeammateAction) error {
	s.actions <- action
	return nil
}
func (s *teammateTerminalSession) SubscribeTeammateEvents(Emit) func() { return func() {} }
func TestActualTerminalTeammateKeysUseDedicatedControllerActionsAndJoinObserver(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	var output lockedBuffer
	session := &teammateTerminalSession{testSession: &testSession{}, ready: make(chan struct{}), actions: make(chan TeammateAction, 10)}
	done := make(chan error, 1)
	go func() { done <- Run(ctx, session, testOptions(reader, &output)) }()
	select {
	case <-session.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("not subscribed")
	}
	// CSI modifier2 is Shift. These bytes pass the real terminal key decoder.
	_, _ = writer.Write([]byte("\x1b[1;2B\x1b[1;2B\r"))
	await := func(kind string) TeammateAction {
		t.Helper()
		deadline := time.NewTimer(3 * time.Second)
		defer deadline.Stop()
		for {
			select {
			case action := <-session.actions:
				if action.Kind == kind {
					return action
				}
			case <-deadline.C:
				t.Fatal("actual terminal controller action absent", kind, output.String())
				return TeammateAction{}
			}
		}
	}
	if action := await("enter"); action.TaskID != "actual-task" {
		t.Fatal(action)
	}
	_, _ = writer.Write([]byte("REAL_TYPED_PEER_MESSAGE\r"))
	if action := await("send"); action.Message != "REAL_TYPED_PEER_MESSAGE" {
		t.Fatal(action)
	}
	_, _ = writer.Write([]byte("\x1b"))
	if action := await("interrupt"); action.TaskID != "actual-task" {
		t.Fatal(action)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("terminal observer did not join")
	}
	if !session.removed.Load() || session.closed.Load() != 1 {
		t.Fatal("real terminal lifecycle leaked")
	}
}
