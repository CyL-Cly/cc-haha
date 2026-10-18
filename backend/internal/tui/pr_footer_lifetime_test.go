package tui

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

type prFooterExitSession struct {
	testSession
	started, prepared, cancelled, cleanup chan struct{}
}

func (*prFooterExitSession) TerminalPreferences(context.Context) (TerminalPreferences, error) {
	return TerminalPreferences{PRStatusFooterEnabled: true}, nil
}
func (s *prFooterExitSession) FetchTerminalPRStatus(ctx context.Context) (json.RawMessage, error) {
	close(s.started)
	<-ctx.Done()
	close(s.cancelled)
	<-s.cleanup
	return nil, ctx.Err()
}
func (s *prFooterExitSession) PrepareWorktreeExit(context.Context) (*WorktreeExitPrompt, error) {
	close(s.prepared)
	return nil, nil
}
func (*prFooterExitSession) CompleteWorktreeExit(context.Context, string) (string, error) {
	return "", nil
}

func TestTerminalPRFooterNativeWorkerDoesNotBlockExitAndJoinsBeforeSessionClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	session := &prFooterExitSession{started: make(chan struct{}), prepared: make(chan struct{}), cancelled: make(chan struct{}), cleanup: make(chan struct{})}
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
		t.Fatal("Session.Close overtook actual footer worker cleanup")
	}
	close(session.cleanup)
	if err := await(t, returned); err != nil {
		t.Fatal("normal footer exit", err)
	}
	if session.closed.Load() != 1 {
		t.Fatal("footer worker wasn't joined before Session.Close")
	}
}
