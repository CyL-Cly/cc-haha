package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/charmbracelet/x/term"
)

type Options struct {
	Input          io.Reader
	Output         io.Writer
	Environment    []string
	Title          string
	InitialPrompt  string
	InitialHistory []protocol.JSONObject
	Commands       []Completion
	// Keybindings is the host-resolved default+user set. A nil set uses Source
	// external-build defaults. A non-nil empty set disables configurable keys.
	Keybindings []Keybinding
	Width       int
	Height      int
	Fullscreen  bool
	// ExternalEditor is selected by the host from its editor configuration.
	// The terminal layer never inherits an implicit process environment.
	ExternalEditor *ExternalEditor
	// OnResize observes the actual terminal dimensions for host telemetry.
	// It does not participate in Agent execution or alter submitted input.
	OnResize func(width, height int)
}

type eventMsg struct {
	event Event
	ack   chan struct{}
}
type returnedMsg struct {
	id  string
	err error
}
type permissionReturnedMsg struct{ err error }
type actionEventMsg struct {
	event Event
	ack   chan struct{}
}
type actionReturnedMsg struct{ err error }
type editorReturnedMsg struct {
	text          string
	changed       bool
	err           error
	permission    string
	questionIndex int
}
type tickMsg time.Time
type foregroundTasksChangedMsg bool
type permissionModeChangedMsg string
type commandsChangedMsg []Completion
type inputEndMsg struct{}

var errTerminalInputClosed = errors.New("terminal input reached EOF")

type terminalModel struct {
	state           *State
	session         Session
	ctx             context.Context
	program         *tea.Program
	options         Options
	lifetime        sync.Mutex
	closed          bool
	worktreeChecked bool
	inputEnded      bool
	turns           sync.WaitGroup
	activeTurns     int
	turnsDrained    chan struct{}
}

func (m *terminalModel) beginTurn() bool {
	m.lifetime.Lock()
	defer m.lifetime.Unlock()
	if m.closed || m.ctx.Err() != nil {
		return false
	}
	if m.activeTurns == 0 {
		m.turnsDrained = make(chan struct{})
	}
	m.activeTurns++
	m.turns.Add(1)
	return true
}

func (m *terminalModel) stopStartingTurns() { m.lifetime.Lock(); m.closed = true; m.lifetime.Unlock() }

func (m *terminalModel) Init() tea.Cmd {
	commands := []tea.Cmd{m.tick()}
	if m.options.InitialPrompt != "" {
		m.state.Editor.Set(m.options.InitialPrompt)
		m.state.syncWorkflowKeyword(time.Now())
		commands = append(commands, m.actions(m.state.submit()))
	}
	return tea.Batch(commands...)
}

func (m *terminalModel) tick() tea.Cmd {
	return func() tea.Msg {
		timer := time.NewTimer(200 * time.Millisecond)
		defer timer.Stop()
		select {
		case now := <-timer.C:
			return tickMsg(now)
		case <-m.ctx.Done():
			return nil
		}
	}
}

func (m *terminalModel) actions(actions []action) tea.Cmd {
	commands := make([]tea.Cmd, 0, len(actions))
	for _, action := range actions {
		a := action
		switch a.kind {
		case actionSubmit:
			commands = append(commands, func() tea.Msg {
				// Commands can be dispatched asynchronously while Quit is being
				// processed. Never start a Session turn after terminal teardown.
				if !m.beginTurn() {
					return returnedMsg{a.input.TurnID, context.Canceled}
				}
				defer m.endTurn()
				err := m.session.Submit(m.ctx, a.input, func(event Event) error {
					if event.TurnID == "" && event.Kind != PermissionAsked && event.Kind != PermissionClosed {
						event.TurnID = a.input.TurnID
					}
					ack := make(chan struct{})
					m.program.Send(eventMsg{event, ack})
					select {
					case <-ack:
						return nil
					case <-m.ctx.Done():
						return m.ctx.Err()
					}
				})
				return returnedMsg{a.input.TurnID, err}
			})
		case actionCancel:
			// Cancellation cannot race behind Quit or a later queued submission.
			m.session.Cancel()
		case actionPermission:
			commands = append(commands, func() tea.Msg {
				return permissionReturnedMsg{m.session.RespondPermission(m.ctx, a.permission, a.decision)}
			})
		case actionQuit:
			m.stopStartingTurns()
			if host, ok := m.session.(WorktreeExitSession); ok && !m.worktreeChecked && !m.inputEnded {
				m.worktreeChecked = true
				commands = append(commands, m.prepareExitAfterTurns(host))
			} else {
				commands = append(commands, m.quitWithoutWorktree())
			}
		case actionSuspend:
			commands = append(commands, tea.Suspend)
		case actionRedraw:
			commands = append(commands, tea.ClearScreen)
		case actionEditor:
			if m.options.ExternalEditor == nil {
				commands = append(commands, func() tea.Msg { return editorReturnedMsg{err: errors.New("External editor is not configured")} })
				continue
			}
			editor := newEditorExecution(m.ctx, *m.options.ExternalEditor, a.text, m.options.Input, m.options.Output)
			commands = append(commands, tea.Exec(editor, func(err error) tea.Msg {
				return editorReturnedMsg{text: editor.text, changed: editor.changed, err: err, permission: a.permission, questionIndex: a.questionIndex}
			}))
		case actionInvoke:
			commands = append(commands, func() tea.Msg {
				if !m.beginTurn() {
					return actionReturnedMsg{context.Canceled}
				}
				defer m.endTurn()
				host, ok := m.session.(ActionSession)
				if !ok {
					return actionReturnedMsg{&UnsupportedAction{Action: a.name}}
				}
				err := host.InvokeAction(m.ctx, a.name, func(event Event) error {
					ack := make(chan struct{})
					m.program.Send(actionEventMsg{event: event, ack: ack})
					select {
					case <-ack:
						return nil
					case <-m.ctx.Done():
						return m.ctx.Err()
					}
				})
				return actionReturnedMsg{err}
			})
		case actionModelPicker:
			commands = append(commands, func() tea.Msg {
				if !m.beginTurn() {
					return actionReturnedMsg{context.Canceled}
				}
				defer m.endTurn()
				host, ok := m.session.(ModelPickerSession)
				if !ok {
					return actionReturnedMsg{&UnsupportedAction{Action: "modelPicker:select"}}
				}
				err := host.RespondModelPicker(m.ctx, a.permission, a.modelPickerResult, func(event Event) error {
					ack := make(chan struct{})
					m.program.Send(actionEventMsg{event: event, ack: ack})
					select {
					case <-ack:
						return nil
					case <-m.ctx.Done():
						return m.ctx.Err()
					}
				})
				return actionReturnedMsg{err}
			})
		case actionThinkingPicker:
			commands = append(commands, m.thinkingPickerCommand(a))
		case actionTeammate:
			commands = append(commands, m.teammateCommand(a.teammate))
		}
	}
	return tea.Batch(commands...)
}

func (m *terminalModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case teammatesChangedMsg:
		m.state.SetTeammateTasks(msg.tasks)
	case permissionModeChangedMsg:
		m.state.PermissionMode = string(msg)
	case worktreePreparedMsg:
		return m, m.openWorktreeDialog(msg)
	case worktreeCompletedMsg:
		if msg.err != nil {
			m.state.Status = msg.err.Error()
			m.state.Closing = false
			m.state.WorktreeExit = nil
			m.worktreeChecked = false
			return m, nil
		}
		if msg.message != "" {
			m.state.Entries = append(m.state.Entries, Entry{Kind: "notice", Text: msg.message})
		}
		return m, tea.Quit
	case foregroundTasksChangedMsg:
		m.state.ForegroundTasks = bool(msg)
	case commandsChangedMsg:
		m.state.Apply(Event{Kind: CommandsChanged, Commands: []Completion(msg)})
		if source, ok := m.session.(WorkflowKeywordSource); ok {
			return m, func() tea.Msg {
				enabled, err := source.WorkflowKeywordEnabled(m.ctx)
				return workflowKeywordEnabledMsg{enabled, err}
			}
		}
	case workflowKeywordEnabledMsg:
		if msg.err != nil {
			m.state.Status = msg.err.Error()
		} else {
			m.state.SetWorkflowKeywordEnabled(msg.enabled, time.Now())
		}
	case tea.KeyPressMsg:
		if m.state.WorktreeExit != nil {
			return m, m.handleWorktreeKey(terminalKey(msg))
		}
		return m, m.actions(m.state.HandleKey(terminalKey(msg), time.Now()))
	case tea.PasteMsg:
		m.state.Paste(msg.Content)
	case tea.WindowSizeMsg:
		m.state.Resize(msg.Width, msg.Height)
		if m.options.OnResize != nil {
			m.options.OnResize(m.state.Width, m.state.Height)
		}
	case eventMsg:
		if msg.event.Kind == TeammateTurnFinished {
			close(msg.ack)
			return m, m.actions(m.state.FinishTeammateTurn(msg.event))
		}
		if msg.event.Kind == ModelPickerOpened && msg.event.ModelPicker != nil {
			m.state.OpenModelPicker(*msg.event.ModelPicker)
		}
		if msg.event.Kind == ModelPickerClosed {
			m.state.ModelPicker = nil
		}
		m.state.Apply(msg.event)
		close(msg.ack)
	case returnedMsg:
		return m, m.actions(m.state.FinishTurn(msg.id, msg.err))
	case permissionReturnedMsg:
		if msg.err != nil {
			m.state.Status = msg.err.Error()
		}
	case actionEventMsg:
		if msg.event.Kind == TeammateTurnFinished {
			if msg.ack != nil {
				close(msg.ack)
			}
			return m, m.actions(m.state.FinishTeammateTurn(msg.event))
		}
		if msg.event.Kind == ModelPickerOpened && msg.event.ModelPicker != nil {
			m.state.OpenModelPicker(*msg.event.ModelPicker)
		} else if msg.event.Kind == ModelPickerClosed {
			m.state.ModelPicker = nil
		} else if msg.event.Kind == Notice {
			m.state.applyNotice(msg.event)
		} else {
			m.state.Apply(msg.event)
		}
		if msg.ack != nil {
			close(msg.ack)
		}
	case actionReturnedMsg:
		if msg.err != nil {
			m.state.Status = msg.err.Error()
		}
	case editorReturnedMsg:
		m.state.Editing = false
		if msg.err != nil {
			m.state.Status = msg.err.Error()
		} else if msg.permission != "" {
			m.state.finishQuestionEditor(msg.permission, msg.questionIndex, msg.text, msg.changed)
		} else if msg.changed {
			m.state.Editor.snapshot()
			m.state.Editor.Set(recollapsePastedText(msg.text, m.state.PastedContents))
			m.state.Status = ""
			m.state.updateCompletions()
		}
	case inputEndMsg:
		m.inputEnded = true
		if m.state.WorktreeExit != nil {
			return m, tea.Quit
		}
		return m, m.actions(m.state.EOF())
	case tickMsg:
		m.state.Tick(time.Time(msg))
		return m, m.tick()
	}
	return m, nil
}

func (m *terminalModel) View() tea.View {
	return m.state.View(m.options.Title, m.options.Fullscreen)
}

type eofReader struct {
	io.Reader
	pendingEOF bool
}

func (r *eofReader) Read(buffer []byte) (int, error) {
	if r.pendingEOF {
		return 0, errTerminalInputClosed
	}
	n, err := r.Reader.Read(buffer)
	if errors.Is(err, io.EOF) {
		r.pendingEOF = true
		// Ultraviolet flushes its parser before returning a non-EOF error.
		// Plain io.EOF is swallowed by its scanner, which would leave the TUI
		// running forever. An independent EOF goroutine could also overtake
		// buffered keys. Deliver bytes first, then this ordered terminal event.
		if n > 0 {
			return n, nil
		}
		return 0, errTerminalInputClosed
	}
	return n, err
}

// eofFileReader preserves the descriptor interface. Bubble Tea must see the
// actual TTY in order to enter raw mode and restore its original termios state.
type eofFileReader struct {
	*eofReader
	file *os.File
}

func (r *eofFileReader) Fd() uintptr                      { return r.file.Fd() }
func (r *eofFileReader) Write(buffer []byte) (int, error) { return r.file.Write(buffer) }
func (r *eofFileReader) Close() error                     { return r.file.Close() }
func (r *eofFileReader) Name() string                     { return r.file.Name() }

type savedTerminal struct {
	file  term.File
	state *term.State
}

func captureTerminals(input io.Reader, output io.Writer) ([]savedTerminal, error) {
	var states []savedTerminal
	for _, stream := range []any{input, output} {
		if file, ok := stream.(term.File); ok && term.IsTerminal(file.Fd()) {
			state, err := term.GetState(file.Fd())
			if err != nil {
				return nil, err
			}
			states = append(states, savedTerminal{file, state})
		}
	}
	return states, nil
}

// Run owns terminal lifetime, not Agent lifetime. The supplied interactive
// Session owns the same shared Agent core used by the CLI/Server hosts.
func Run(ctx context.Context, session Session, options Options) (err error) {
	if ctx == nil || session == nil || options.Input == nil || options.Output == nil {
		return errors.New("TUI requires a context, shared Session and explicit terminal input/output")
	}
	ctx, cancel := context.WithCancel(ctx)
	var terminals []savedTerminal
	var model *terminalModel
	defer func() {
		if model != nil {
			model.stopStartingTurns()
		}
		cancel()
		session.Cancel()
		// Bubble Tea restores its raw mode internally, but discards restore
		// errors. Restore the caller's exact native state and report failures.
		// Kernel input-queue flags continue to follow native terminal I/O;
		// restoring configuration must not read or discard pending user input.
		for _, terminal := range terminals {
			err = errors.Join(err, term.Restore(terminal.file.Fd(), terminal.state))
		}
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer closeCancel()
		if model != nil {
			done := make(chan struct{})
			go func() { model.turns.Wait(); close(done) }()
			select {
			case <-done:
			case <-closeCtx.Done():
				err = errors.Join(err, closeCtx.Err())
			}
		}
		var resumeHint string
		if host, ok := session.(ResumeHintSession); ok {
			if output, ok := options.Output.(term.File); ok && term.IsTerminal(output.Fd()) {
				// Capture before the actual Owner closes, then print after shared
				// cleanup and renderer teardown on the restored main buffer.
				resumeHint, _ = host.ResumeHint(closeCtx)
			}
		}
		if host, ok := session.(ExitReasonSession); ok && model != nil && model.state.Closing {
			err = errors.Join(err, host.CloseWithReason(closeCtx, "prompt_input_exit"))
		} else {
			err = errors.Join(err, session.Close(closeCtx))
		}
		if resumeHint != "" {
			// Source treats a disappearing terminal during shutdown as best
			// effort. A hint write must not change the process exit status.
			_, _ = io.WriteString(options.Output, "\x1b[2m"+resumeHint+"\x1b[22m")
		}
	}()
	terminals, err = captureTerminals(options.Input, options.Output)
	if err != nil {
		return err
	}
	reader := &eofReader{Reader: options.Input}
	var input io.Reader = reader
	if file, ok := options.Input.(*os.File); ok {
		input = &eofFileReader{eofReader: reader, file: file}
	}
	width, height := options.Width, options.Height
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	model = &terminalModel{state: NewState(options.Commands, width, height), session: session, ctx: ctx, options: options}
	if source, ok := session.(WorkflowKeywordSource); ok {
		enabled, e := source.WorkflowKeywordEnabled(ctx)
		if e != nil {
			return e
		}
		model.state.SetWorkflowKeywordEnabled(enabled, time.Now())
	}
	model.state.SetInitialHistory(options.InitialHistory, options.Fullscreen)
	if options.Keybindings != nil {
		model.state.SetKeybindings(options.Keybindings)
	}
	// Bubble Tea's killed shutdown closes the BSD cancel reader without joining
	// its read loop. Closing its signal pipe can race with the reader's Fd call.
	// Keep Agent cancellation on ctx, but ask the renderer to quit normally so
	// its cancellation signal is consumed and its reader is joined before Close.
	// Native termination signals follow the same ordered shutdown path.
	program := tea.NewProgram(model, tea.WithContext(context.WithoutCancel(ctx)), tea.WithoutSignalHandler(), tea.WithInput(input), tea.WithOutput(options.Output), tea.WithEnvironment(options.Environment), tea.WithWindowSize(width, height))
	model.program = program
	if remove, e := model.bindTeammates(); e != nil {
		return e
	} else if remove != nil {
		defer remove()
	}
	if source, ok := session.(PermissionModeSource); ok {
		initial, unsubscribe := source.SubscribePermissionMode(func(mode string) { program.Send(permissionModeChangedMsg(mode)) })
		if unsubscribe != nil {
			defer unsubscribe()
		}
		model.state.PermissionMode = initial
	}
	if source, ok := session.(CommandSource); ok {
		initial, unsubscribe := source.SubscribeCommands(func(commands []Completion) {
			program.Send(commandsChangedMsg(CloneCompletions(commands)))
		})
		if unsubscribe != nil {
			defer unsubscribe()
		}
		model.state.Apply(Event{Kind: CommandsChanged, Commands: initial})
	}
	if source, ok := session.(EnvironmentHookSource); ok {
		unsubscribe := source.SubscribeEnvironmentHookNotices(func(event Event) { program.Send(actionEventMsg{event: event}) })
		if unsubscribe != nil {
			defer unsubscribe()
		}
	}
	if source, ok := session.(ForegroundTaskSource); ok {
		var latest atomic.Bool
		wake := make(chan struct{}, 1)
		current, unsubscribe, e := source.SubscribeForegroundTasks(ctx, func(active bool) {
			latest.Store(active)
			select {
			case wake <- struct{}{}:
			default:
			}
		})
		if e != nil {
			return e
		}
		model.state.ForegroundTasks = current
		relayCtx, stopRelay := context.WithCancel(ctx)
		relayDone := make(chan struct{})
		go func() {
			defer close(relayDone)
			for {
				select {
				case <-relayCtx.Done():
					return
				case <-wake:
					program.Send(foregroundTasksChangedMsg(latest.Load()))
				}
			}
		}()
		defer func() {
			if unsubscribe != nil {
				unsubscribe()
			}
			stopRelay()
			<-relayDone
		}()
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	stopWatching := make(chan struct{})
	watchDone := make(chan struct{})
	terminated := false // Read only after watchDone joins the signal observer.
	var signalError error
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			program.Quit()
		case received := <-signals:
			terminated = true
			if received == os.Interrupt {
				signalError = tea.ErrInterrupted
			}
			cancel()
			program.Quit()
		case <-stopWatching:
		}
	}()
	_, err = program.Run()
	close(stopWatching)
	<-watchDone
	if errors.Is(err, errTerminalInputClosed) {
		err = nil
	}
	if terminated {
		err = errors.Join(err, signalError)
	} else if ctx.Err() != nil {
		err = errors.Join(err, ctx.Err())
	}
	return err
}

// UnsupportedAction retains a distinct capability boundary when an interactive
// host has not supplied a real consumer; the command cannot reach the model.
type UnsupportedAction struct{ Action string }

func (e *UnsupportedAction) Error() string {
	return fmt.Sprintf("Interactive action %s is not implemented by this Session", e.Action)
}

func terminalKey(msg tea.KeyPressMsg) Key {
	key := Key{Name: msg.String(), Text: msg.Text}
	stroke := keyStroke(key)
	if stroke != nil {
		stroke.Ctrl = msg.Mod.Contains(tea.ModCtrl)
		stroke.Shift = msg.Mod.Contains(tea.ModShift)
		stroke.Alt = msg.Mod.Contains(tea.ModAlt)
		stroke.Meta = msg.Mod.Contains(tea.ModMeta)
		stroke.Super = msg.Mod.Contains(tea.ModSuper)
		if stroke.Key == "escape" {
			stroke.Alt, stroke.Meta = false, false
		}
		key.Stroke = stroke
	}
	return key
}
