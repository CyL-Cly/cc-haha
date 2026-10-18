package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
)

func TestActualPTYUnicodePasteApprovalCancellationResizeAndRestore(t *testing.T) {
	testUnixPTY(t, "interaction")
}

func TestActualPTYContextCancellationRestoresTerminal(t *testing.T) {
	testUnixPTY(t, "context-cancel")
}

func TestActualPTYTerminationSignalRestoresTerminal(t *testing.T) {
	testUnixPTY(t, "signal")
}

func TestActualPTYExternalEditorHandoffRestoresTerminal(t *testing.T) { testUnixPTY(t, "editor") }
func TestActualPTYConfiguredKeysAndHostCommandPreserveDraft(t *testing.T) {
	testUnixPTY(t, "keybindings")
}
func TestActualPTYModelPickerCancelEffortSelectAndPreservedDraft(t *testing.T) {
	testUnixPTY(t, "model-picker")
}
func TestActualPTYForegroundTaskBackgroundAndReadlineRestoration(t *testing.T) {
	testUnixPTY(t, "foreground-task")
}

func testUnixPTY(t *testing.T, scenario string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("requires the Windows ConPTY lane; Unix PTY evidence is not Windows evidence")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is required by the native PTY driver")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err = os.Mkdir(filepath.Join(home, ".fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, ".fixture", "tui-pty-home"), []byte("disposable TUI PTY home\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, "testdata/pty.py", binary, home, scenario)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "TUI_PTY_HELPER=1", "TUI_PTY_SCENARIO=" + scenario}
	capturePublicGoCoverage(t, command)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("native PTY failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `"terminalRestored": true`) {
		t.Fatalf("PTY did not verify terminal restoration: %s", output)
	}
}

type fixtureSession struct {
	mu               sync.Mutex
	turns            sync.WaitGroup
	home             string
	cancel           chan struct{}
	decision         chan PermissionDecision
	originalTerminal *term.State
	originalOutput   *term.State
	stop             context.CancelFunc
	foreground       func(bool)
	promotion        chan struct{}
}

func (s *fixtureSession) record(value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := os.OpenFile(filepath.Join(s.home, ".fixture", "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	if err = json.NewEncoder(file).Encode(value); err != nil {
		panic(err)
	}
}

func (s *fixtureSession) Submit(ctx context.Context, input Input, emit Emit) error {
	s.turns.Add(1)
	defer s.turns.Done()
	cancel := make(chan struct{})
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.cancel = nil; s.mu.Unlock() }()
	s.record(map[string]any{"kind": "submit", "input": input})
	if input.Text == "foreground" && os.Getenv("TUI_PTY_SCENARIO") == "foreground-task" {
		s.mu.Lock()
		callback := s.foreground
		s.mu.Unlock()
		if callback == nil {
			return errors.New("foreground task source is not subscribed")
		}
		callback(true)
		if err := emit(Event{Kind: TextDelta, Text: "FG_TASK_REGISTERED"}); err != nil {
			return err
		}
		select {
		case <-s.promotion:
		case <-ctx.Done():
			return ctx.Err()
		}
		callback(false)
		return emit(Event{Kind: TextDelta, Text: "FG_BACKGROUND_OK"})
	}
	if input.Text == "context-cancel" {
		if err := emit(Event{Kind: TextDelta, Text: "partial_before_context_cancel"}); err != nil {
			return err
		}
		s.stop()
		<-ctx.Done()
		s.record(map[string]any{"kind": "context-cancelled"})
		return ctx.Err()
	}
	if input.Text == "cancel" {
		if err := emit(Event{Kind: TextDelta, Text: "partial_cancel 中文"}); err != nil {
			return err
		}
		select {
		case <-cancel:
			s.record(map[string]any{"kind": "cancelled"})
			return context.Canceled
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := emit(Event{Kind: TextDelta, Text: "stream_中文_👋"}); err != nil {
		return err
	}
	if err := emit(Event{Kind: ToolStarted, ToolID: "write-1", ToolName: "Write", Input: json.RawMessage(`{"file_path":"fixture.txt","content":"hello"}`)}); err != nil {
		return err
	}
	if err := emit(Event{Kind: PermissionAsked, Permission: &PermissionRequest{ID: "permit-1", ToolName: "Write", Description: "fixture approval", Input: json.RawMessage(`{"file_path":"fixture.txt","content":"hello"}`)}}); err != nil {
		return err
	}
	s.record(map[string]any{"kind": "permission-ready"})
	select {
	case decision := <-s.decision:
		if !decision.Allowed {
			return errors.New("fixture permission denied")
		}
	case <-cancel:
		return context.Canceled
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := emit(Event{Kind: ToolFinished, ToolID: "write-1", Output: json.RawMessage(`{"success":true}`)}); err != nil {
		return err
	}
	if err := emit(Event{Kind: TextDelta, Text: "FILE_CREATED"}); err != nil {
		return err
	}
	s.record(map[string]any{"kind": "finished"})
	return nil
}

func (s *fixtureSession) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		select {
		case <-s.cancel:
		default:
			close(s.cancel)
		}
	}
}
func (s *fixtureSession) RespondPermission(ctx context.Context, id string, decision PermissionDecision) error {
	s.record(map[string]any{"kind": "decision", "id": id, "allowed": decision.Allowed})
	select {
	case s.decision <- decision:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *fixtureSession) InvokeAction(ctx context.Context, name string, emit Emit) error {
	s.record(map[string]any{"kind": "action", "name": name})
	if os.Getenv("TUI_PTY_SCENARIO") == "foreground-task" {
		if name != "task:background" {
			return fmt.Errorf("unexpected foreground action: %s", name)
		}
		close(s.promotion)
		return nil
	}
	if os.Getenv("TUI_PTY_SCENARIO") == "model-picker" {
		a, b, effort := "sonnet", "opus", "medium"
		return emit(Event{Kind: ModelPickerOpened, ModelPicker: &ModelPicker{ID: "native-picker", Initial: &a, Effort: &effort, Options: []ModelOption{{Label: "Default", DefaultEffort: "high"}, {Value: &a, Label: "Native Sonnet", SupportsEffort: true, DefaultEffort: "high"}, {Value: &b, Label: "Native Opus", SupportsEffort: true, SupportsMax: true, DefaultEffort: "high"}}}})
	}
	return emit(Event{Kind: Notice, Text: "HOST_COMMAND_ONLY 中文"})
}
func (s *fixtureSession) SubscribeForegroundTasks(_ context.Context, callback func(bool)) (bool, func(), error) {
	s.mu.Lock()
	s.foreground = callback
	s.mu.Unlock()
	return false, func() {
		s.mu.Lock()
		s.foreground = nil
		s.mu.Unlock()
		if os.Getenv("TUI_PTY_SCENARIO") == "foreground-task" {
			s.record(map[string]any{"kind": "foreground-unsubscribed"})
		}
	}, nil
}
func (s *fixtureSession) RespondModelPicker(ctx context.Context, id string, result ModelPickerResult, emit Emit) error {
	s.record(map[string]any{"kind": "model-selection", "id": id, "result": result})
	if result.Cancelled {
		return nil
	}
	return emit(Event{Kind: Notice, Text: "MODEL_SELECTED_ORIGINAL_HOST"})
}
func (s *fixtureSession) Close(ctx context.Context) error {
	done := make(chan struct{})
	go func() { s.turns.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	beforeProbe, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		return err
	}
	pending, err := settleTerminalForObservation(os.Stdin.Fd())
	if err != nil {
		return err
	}
	after, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		return err
	}
	afterOutput, err := term.GetState(os.Stdout.Fd())
	if err != nil {
		return err
	}
	restored := reflect.DeepEqual(s.originalTerminal, after) && reflect.DeepEqual(s.originalOutput, afterOutput)
	s.record(map[string]any{"kind": "terminal-state-restored", "restored": restored,
		"entryState": fmt.Sprintf("%+v", s.originalTerminal), "afterRunBeforeNativeProbe": fmt.Sprintf("%+v", beforeProbe),
		"afterNativeProbe": fmt.Sprintf("%+v", after), "nativePendingReadBytes": pending,
		"entryOutputState": fmt.Sprintf("%+v", s.originalOutput), "afterOutputState": fmt.Sprintf("%+v", afterOutput)})
	s.record(map[string]any{"kind": "closed"})
	if !restored {
		return fmt.Errorf("terminal native state was not restored: before=%+v after=%+v", s.originalTerminal, after)
	}
	return nil
}

func TestTerminalFixtureProcess(t *testing.T) {
	if os.Getenv("TUI_PTY_HELPER") != "1" {
		t.Skip("subprocess-only native terminal fixture")
	}
	home := os.Getenv("HOME")
	marker, err := os.ReadFile(filepath.Join(home, ".fixture", "tui-pty-home"))
	if err != nil || string(marker) != "disposable TUI PTY home\n" {
		t.Fatal("fixture requires its disposable HOME marker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	session := &fixtureSession{home: home, decision: make(chan PermissionDecision, 1), promotion: make(chan struct{}), stop: cancel}
	session.originalTerminal, err = term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	session.originalOutput, err = term.GetState(os.Stdout.Fd())
	if err != nil {
		t.Fatal(err)
	}
	options := Options{Input: os.Stdin, Output: os.Stdout, Environment: []string{"TERM=xterm-256color", "TERM_PROGRAM=Apple_Terminal"}, Title: "Go TUI fixture", Fullscreen: true, OnResize: func(width, height int) {
		session.record(map[string]any{"kind": "resize", "width": width, "height": height})
	}}
	if os.Getenv("TUI_PTY_SCENARIO") == "keybindings" {
		options.Keybindings = append(DefaultKeybindings("macos", true, KeybindingFeatures{}), binding("Chat", "alt+m", "command:model"), binding("Chat", "alt+u", "chat:undo"), Keybinding{Context: "Chat", Chord: ParseChord("ctrl+s")})
	}
	if os.Getenv("TUI_PTY_SCENARIO") == "model-picker" {
		options.Keybindings = append(DefaultKeybindings("macos", true, KeybindingFeatures{}), binding("Chat", "alt+p", "chat:modelPicker"))
	}
	if os.Getenv("TUI_PTY_SCENARIO") == "editor" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		options.ExternalEditor = &ExternalEditor{Command: []string{binary, "-test.run=^TestExternalEditorFixtureProcess$", "-test.count=1"}, TempDir: home, Environment: append(os.Environ(), "TUI_EDITOR_HELPER=1", "TUI_EDITOR_TERMINAL=1", "TUI_EDITOR_CONTENT=edited 中文\n")}
	}
	if err = Run(ctx, session, options); err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() == context.Canceled {
			session.record(map[string]any{"kind": "expected-context-error"})
			return
		}
		t.Fatal(fmt.Errorf("terminal fixture: %w", err))
	}
}
