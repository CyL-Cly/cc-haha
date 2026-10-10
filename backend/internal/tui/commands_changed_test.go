package tui

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCommandChangesRefreshIdleAndBusyCompletionWithoutChangingDraft(t *testing.T) {
	s := NewState([]Completion{{Name: "old"}}, 80, 24)
	s.Editor.Set("/comp")
	s.Editor.snapshot()
	commands := []Completion{{Name: "computer-use", Description: "native app skill"}}
	s.Apply(Event{Kind: CommandsChanged, Commands: commands})
	commands[0].Name = "mutated caller slice"
	if s.Editor.Text() != "/comp" || len(s.Completions) != 1 || s.Completions[0].Name != "computer-use" {
		t.Fatal("idle menu did not update or altered draft", s.Completions)
	}
	s.ActiveTurn = "active"
	s.Apply(Event{Kind: CommandsChanged, Commands: nil})
	if len(s.Completions) != 0 || s.Editor.Text() != "/comp" || s.ActiveTurn != "active" {
		t.Fatal("busy command update changed turn/draft")
	}
}

type commandTestSession struct {
	*testSession
	mu           sync.Mutex
	callback     func([]Completion)
	ready        chan struct{}
	unsubscribed bool
}

func (s *commandTestSession) SubscribeCommands(callback func([]Completion)) ([]Completion, func()) {
	s.mu.Lock()
	s.callback = callback
	s.mu.Unlock()
	close(s.ready)
	return []Completion{{Name: "old"}}, func() { s.mu.Lock(); defer s.mu.Unlock(); s.callback = nil; s.unsubscribed = true }
}
func TestRunReceivesIdleCommandSignalAndUnsubscribesBeforeSessionClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	var output lockedBuffer
	s := &commandTestSession{testSession: &testSession{}, ready: make(chan struct{})}
	returned := make(chan error, 1)
	go func() { returned <- Run(ctx, s, testOptions(reader, &output)) }()
	select {
	case <-s.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("not subscribed")
	}
	_, _ = writer.Write([]byte("/computer"))
	s.mu.Lock()
	callback := s.callback
	s.mu.Unlock()
	callback([]Completion{{Name: "computer-use", Description: "physical_file_skill_change"}})
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(output.String(), "physical_file_skill_change") {
		if time.Now().After(deadline) {
			t.Fatal("terminal did not render idle command update", output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-returned:
	case <-time.After(3 * time.Second):
		t.Fatal("terminal did not close")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.unsubscribed || s.callback != nil || s.closed.Load() != 1 {
		t.Fatal("command listener survived terminal shutdown")
	}
}
