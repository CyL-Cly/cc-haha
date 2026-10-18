package tui

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

type terminalUpdateTimingSession struct {
	testSession
	calls int
}

type terminalUpdateExitSession struct {
	testSession
	started, prepared, cancelled, cleanup chan struct{}
}

func (s *terminalUpdateExitSession) CheckTerminalUpdate(ctx context.Context, _ Emit) error {
	close(s.started)
	<-ctx.Done()
	close(s.cancelled)
	<-s.cleanup
	return ctx.Err()
}
func (s *terminalUpdateExitSession) PrepareWorktreeExit(context.Context) (*WorktreeExitPrompt, error) {
	close(s.prepared)
	return nil, nil
}
func (s *terminalUpdateExitSession) CompleteWorktreeExit(context.Context, string) (string, error) {
	return "", nil
}

func TestTerminalUpdaterDoesNotBlockWorktreeExitAndJoinsBeforeSessionClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	session := &terminalUpdateExitSession{started: make(chan struct{}), prepared: make(chan struct{}), cancelled: make(chan struct{}), cleanup: make(chan struct{})}
	var output lockedBuffer
	returned := make(chan error, 1)
	go func() { returned <- Run(ctx, session, testOptions(reader, &output)) }()
	await(t, session.started)
	if _, err := writer.Write([]byte("/exit\r")); err != nil {
		t.Fatal(err)
	}
	await(t, session.prepared)
	await(t, session.cancelled)
	if session.closed.Load() != 0 {
		t.Fatal("Session.Close ran before terminal updater cleanup")
	}
	close(session.cleanup)
	if err := await(t, returned); err != nil {
		t.Fatal("normal exit while an update is running failed", err)
	}
	if session.closed.Load() != 1 {
		t.Fatal("terminal updater was not joined before the shared Session closed")
	}
}

func (s *terminalUpdateTimingSession) CheckTerminalUpdate(context.Context, Emit) error {
	s.calls++
	return nil
}
func TestTerminalUpdaterThirtyMinuteGateAndActualSourcePresentation(t *testing.T) {
	now := time.Now()
	session := &terminalUpdateTimingSession{}
	m := terminalModel{session: session, state: NewState(nil, 90, 30), ctx: context.Background(), turnsDrained: make(chan struct{})}
	m.terminalUpdateUntil = now.Add(30 * time.Minute)
	for _, at := range []time.Time{now, now.Add(29*time.Minute + 59*time.Second)} {
		if m.terminalUpdateTick(at) != nil {
			t.Fatal("periodic update fired early")
		}
	}
	command := m.terminalUpdateTick(now.Add(30 * time.Minute))
	if command == nil {
		t.Fatal("missing actual 30-minute command")
	}
	command()
	if session.calls != 1 || m.terminalUpdateTick(now.Add(30*time.Minute+time.Second)) != nil {
		t.Fatal("timer did not advance", session.calls)
	}
	for _, pair := range []struct{ phase, text string }{{"checking", "Checking for updates"}, {"success", "Update installed · Restart to update"}, {"install_failed", "Auto-update failed · Try /status"}, {"", ""}} {
		m.state.Apply(Event{Kind: TerminalUpdateChanged, Result: []byte(`{"phase":"` + pair.phase + `"}`)})
		text := strings.Join(m.state.terminalUpdateLines(), "\n")
		if pair.text == "" && text != "" || !strings.Contains(text, pair.text) {
			t.Fatal(text)
		}
	}
}
