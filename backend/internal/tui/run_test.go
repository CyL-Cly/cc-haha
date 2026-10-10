package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/muesli/cancelreader"
)

type lockedBuffer struct {
	sync.Mutex
	bytes.Buffer
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.Write(data)
}
func (b *lockedBuffer) String() string { b.Lock(); defer b.Unlock(); return b.Buffer.String() }

type testSession struct {
	submit     func(context.Context, Input, Emit) error
	permission func(context.Context, string, PermissionDecision) error
	cancel     func()
	closed     atomic.Int32
}

func (s *testSession) Submit(ctx context.Context, input Input, emit Emit) error {
	if s.submit != nil {
		return s.submit(ctx, input, emit)
	}
	return nil
}
func (s *testSession) Cancel() {
	if s.cancel != nil {
		s.cancel()
	}
}
func (s *testSession) RespondPermission(ctx context.Context, id string, decision PermissionDecision) error {
	if s.permission != nil {
		return s.permission(ctx, id, decision)
	}
	return nil
}
func (s *testSession) Close(context.Context) error { s.closed.Add(1); return nil }

func testOptions(reader io.Reader, output io.Writer) Options {
	return Options{Input: reader, Output: output, Environment: []string{"TERM=dumb", "TERM_PROGRAM=Apple_Terminal"}, Width: 80, Height: 24, Title: "TUI test"}
}

type actionTestSession struct {
	*testSession
	invoke func(context.Context, string, Emit) error
}

func (s *actionTestSession) InvokeAction(ctx context.Context, name string, emit Emit) error {
	return s.invoke(ctx, name, emit)
}

func TestRunNativeKeybindingInvokesSharedHostPreservesDraftAndWaitsForCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	called := make(chan string, 1)
	notice := make(chan struct{})
	drained := make(chan struct{})
	var output lockedBuffer
	var submits atomic.Int32
	base := &testSession{submit: func(context.Context, Input, Emit) error { submits.Add(1); return nil }}
	session := &actionTestSession{testSession: base, invoke: func(ctx context.Context, name string, emit Emit) error {
		called <- name
		if err := emit(Event{Kind: Notice, Text: "shared_host_model_picker 中文"}); err != nil {
			return err
		}
		close(notice)
		<-ctx.Done()
		<-drained
		return ctx.Err()
	}}
	options := testOptions(reader, &output)
	options.Keybindings = []Keybinding{binding("Chat", "alt+p", "chat:modelPicker")}
	returned := make(chan error, 1)
	go func() { returned <- Run(ctx, session, options) }()
	if _, err := writer.Write([]byte("draft 中文\x1bp")); err != nil {
		t.Fatal(err)
	}
	if got := await(t, called); got != "chat:modelPicker" {
		t.Fatal("wrong shared host action", got)
	}
	await(t, notice)
	if submits.Load() != 0 {
		t.Fatal("keybinding started a model submission")
	}
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(output.String(), "shared_host_model_picker 中文") || !strings.Contains(output.String(), "draft 中文") {
		if time.Now().After(deadline) {
			t.Fatal("host notice or original draft missing", output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if base.closed.Load() != 0 {
		t.Fatal("Close ran before interactive action cleanup")
	}
	close(drained)
	if err := await(t, returned); !errors.Is(err, context.Canceled) {
		t.Fatal("action cancellation was not retained", err)
	}
	if base.closed.Load() != 1 || submits.Load() != 0 {
		t.Fatal("shared host lifetime or prompt boundary changed")
	}
}

func TestRunMissingActionConsumerCannotSubmitSlashCommandToModel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	var output lockedBuffer
	var submits atomic.Int32
	session := &testSession{submit: func(context.Context, Input, Emit) error { submits.Add(1); return nil }}
	options := testOptions(reader, &output)
	options.Keybindings = []Keybinding{binding("Chat", "alt+m", "command:model")}
	returned := make(chan error, 1)
	go func() { returned <- Run(ctx, session, options) }()
	if _, err := writer.Write([]byte("\x1bm")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(output.String(), "command:model is not implemented") {
		if time.Now().After(deadline) {
			t.Fatal("missing explicit action capability", output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	writer.Close()
	if err := await(t, returned); err != nil {
		t.Fatal(err)
	}
	if submits.Load() != 0 {
		t.Fatal("unsupported command fell through to model")
	}
}

func await[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("terminal event deadline exceeded")
		var zero T
		return zero
	}
}

func TestRunActualPipeEOFExitsAndClosesSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	writer.Close()
	var output lockedBuffer
	session := &testSession{}
	if err := Run(ctx, session, testOptions(reader, &output)); err != nil {
		t.Fatalf("EOF was not graceful: %v", err)
	}
	if session.closed.Load() != 1 {
		t.Fatal("session was not closed exactly once")
	}
}

func TestShutdownWaitsForSubmittedTurnCleanupBeforeClosingSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	defer writer.Close()
	started := make(chan struct{})
	cancelled := make(chan struct{})
	cleanup := make(chan struct{})
	session := &testSession{submit: func(ctx context.Context, input Input, emit Emit) error {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-cleanup
		return ctx.Err()
	}}
	var output lockedBuffer
	options := testOptions(reader, &output)
	options.InitialPrompt = "active"
	returned := make(chan error, 1)
	go func() { returned <- Run(ctx, session, options) }()
	await(t, started)
	cancel()
	await(t, cancelled)
	if session.closed.Load() != 0 {
		t.Fatal("Session.Close ran before Submit cleanup")
	}
	close(cleanup)
	if err := await(t, returned); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error not preserved: %v", err)
	}
	if session.closed.Load() != 1 {
		t.Fatal("Session.Close did not run exactly once after cleanup")
	}
}

func TestTerminalLifetimeRejectsLateTurnStart(t *testing.T) {
	m := &terminalModel{ctx: context.Background()}
	if !m.beginTurn() {
		t.Fatal("open terminal rejected a turn")
	}
	m.endTurn()
	m.stopStartingTurns()
	if m.beginTurn() {
		t.Fatal("terminal started a shared Session after teardown")
	}
}

func TestRunStreamToolApprovalAndCompletionThroughInputReader(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	var output lockedBuffer
	permissionReady := make(chan struct{})
	decision := make(chan PermissionDecision, 1)
	completed := make(chan struct{})
	var received Input
	session := &testSession{
		submit: func(ctx context.Context, input Input, emit Emit) error {
			received = input
			if err := emit(Event{Kind: TextDelta, Text: "先流出中文👋"}); err != nil {
				return err
			}
			if err := emit(Event{Kind: ToolStarted, ToolID: "write-1", ToolName: "Write", Input: json.RawMessage(`{"content":"hello"}`)}); err != nil {
				return err
			}
			if err := emit(Event{Kind: PermissionAsked, Permission: &PermissionRequest{ID: "approval-1", ToolName: "Write", Description: "write fixture"}}); err != nil {
				return err
			}
			close(permissionReady)
			select {
			case approval := <-decision:
				if !approval.Allowed {
					return errors.New("expected approval")
				}
			case <-ctx.Done():
				return ctx.Err()
			}
			if err := emit(Event{Kind: ToolFinished, ToolID: "write-1", Output: json.RawMessage(`{"success":true}`)}); err != nil {
				return err
			}
			if err := emit(Event{Kind: TextDelta, Text: "complete"}); err != nil {
				return err
			}
			close(completed)
			return nil
		},
		permission: func(ctx context.Context, id string, value PermissionDecision) error {
			if id != "approval-1" {
				return errors.New("wrong permission owner")
			}
			decision <- value
			return nil
		},
	}
	options := testOptions(reader, &output)
	options.InitialPrompt = "hello 中文"
	finished := make(chan error, 1)
	go func() { finished <- Run(ctx, session, options) }()
	await(t, permissionReady)
	if _, err := writer.Write([]byte("y")); err != nil {
		t.Fatal(err)
	}
	await(t, completed)
	writer.Close()
	if err := await(t, finished); err != nil {
		t.Fatal(err)
	}
	if received.Text != "hello 中文" || received.Mode != "prompt" || received.TurnID == "" {
		t.Fatalf("wrong shared Session input: %#v", received)
	}
	if session.closed.Load() != 1 {
		t.Fatal("close was not exactly once")
	}
}

func TestRunEscapeCancelsActiveSessionAndRetainsPartialUntilShutdown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	var output lockedBuffer
	ready := make(chan struct{})
	cancelled := make(chan struct{})
	returned := make(chan struct{})
	var once sync.Once
	session := &testSession{cancel: func() { once.Do(func() { close(cancelled) }) }, submit: func(ctx context.Context, input Input, emit Emit) error {
		if err := emit(Event{Kind: TextDelta, Text: "partial text 中文"}); err != nil {
			return err
		}
		close(ready)
		select {
		case <-cancelled:
		case <-ctx.Done():
			return ctx.Err()
		}
		close(returned)
		return context.Canceled
	}}
	options := testOptions(reader, &output)
	options.InitialPrompt = "cancel test"
	finished := make(chan error, 1)
	go func() { finished <- Run(ctx, session, options) }()
	await(t, ready)
	if _, err := writer.Write([]byte("\x1b")); err != nil {
		t.Fatal(err)
	}
	await(t, returned)
	writer.Close()
	if err := await(t, finished); err != nil {
		t.Fatal(err)
	}
	if session.closed.Load() != 1 {
		t.Fatal("cancelled session was not closed")
	}
}

func TestRunExternalCancellationPropagatesAfterSessionCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	var output lockedBuffer
	ready := make(chan struct{})
	complete := make(chan struct{})
	session := &testSession{submit: func(ctx context.Context, input Input, emit Emit) error {
		close(ready)
		<-ctx.Done()
		close(complete)
		return ctx.Err()
	}}
	options := testOptions(reader, &output)
	options.InitialPrompt = "pending"
	finished := make(chan error, 1)
	go func() { finished <- Run(ctx, session, options) }()
	await(t, ready)
	cancel()
	if err := await(t, finished); !errors.Is(err, context.Canceled) {
		t.Fatalf("missing external cancellation: %v", err)
	}
	await(t, complete)
	if session.closed.Load() != 1 {
		t.Fatal("context cancellation skipped cleanup")
	}
}

func TestTTYDescriptorRetainsCancelableFileInterface(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	wrapper := &eofFileReader{eofReader: &eofReader{Reader: reader}, file: reader}
	var _ cancelreader.File = wrapper
	if wrapper.Fd() != reader.Fd() || wrapper.Name() != reader.Name() {
		t.Fatal("TTY descriptor identity was lost")
	}
}

type bytesAndEOF struct {
	data []byte
	done bool
}

func (r *bytesAndEOF) Read(buffer []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return copy(buffer, r.data), io.EOF
}

func TestEOFPreservesFinalReadBytesBeforeOrderedTermination(t *testing.T) {
	r := &eofReader{Reader: &bytesAndEOF{data: []byte("中文\r")}}
	buffer := make([]byte, 30)
	n, err := r.Read(buffer)
	if err != nil || string(buffer[:n]) != "中文\r" {
		t.Fatalf("EOF dropped buffered input: %q %v", buffer[:n], err)
	}
	n, err = r.Read(buffer)
	if n != 0 || !errors.Is(err, errTerminalInputClosed) {
		t.Fatalf("ordered terminal close missing: %d %v", n, err)
	}
}
