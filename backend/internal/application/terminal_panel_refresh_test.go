package application

import (
	"context"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTerminalPanelQueuedPollCannotReopenClosedActualSessionDialog(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	o.Interactive = true
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	ctx := context.Background()
	opened := 0
	emit := func(event tui.Event) error {
		if event.Kind == tui.LocalPanelOpened {
			opened++
		}
		return nil
	}
	if handled, err := s.InvokeTerminalCommand(ctx, "tasks", "", emit); !handled || err != nil {
		t.Fatal(handled, err)
	}
	s.mu.Lock()
	captured := s.terminalPanel
	s.mu.Unlock()
	if err = s.refreshTerminalPanelIfActive(ctx, captured, emit); err != nil || opened != 2 {
		t.Fatal("active actual timer did not publish", err, opened)
	}
	if err = s.ApplyLocalPanel(ctx, tui.LocalPanelResult{ID: captured.props.ID, Action: "close"}, emit); err != nil {
		t.Fatal(err)
	}
	if err = s.refreshTerminalPanelIfActive(ctx, captured, emit); err != nil || opened != 2 {
		t.Fatal("queued stale timer resurrected closed dialog", err, opened)
	}
	if handled, err := s.InvokeTerminalCommand(ctx, "tasks", "", emit); !handled || err != nil {
		t.Fatal(handled, err)
	}
	if err = s.refreshTerminalPanelIfActive(ctx, captured, emit); err != nil || opened != 3 {
		t.Fatal("old timer replaced next actual dialog", err, opened)
	}
}
