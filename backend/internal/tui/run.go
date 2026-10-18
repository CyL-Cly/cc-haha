package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/charmbracelet/x/term"
)

type Options struct {
	Input              io.Reader
	Output             io.Writer
	Environment        []string
	Title              string
	TerminalTitle      string
	InitialDraft       string
	InitialPrompt      string
	InitialRemoteState *TerminalRemoteState
	InitialHistory     []protocol.JSONObject
	Commands           []Completion
	// Keybindings is the host-resolved default+user set. A nil set uses Source
	// external-build defaults. A non-nil empty set disables configurable keys.
	Keybindings []Keybinding
	Width       int
	Height      int
	Fullscreen  bool
	// ExternalEditor is selected by the host from its editor configuration.
	// The terminal layer never inherits an implicit process environment.
	ExternalEditor *ExternalEditor
	ClipboardImage func(context.Context) (*PastedContent, error)
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
type localCommandReturnedMsg struct {
	token uint64
	err   error
}
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
type keysChangedMsg []Keybinding
type ideOnboardingChangedMsg struct{ props *IDEPicker }
type inputEndMsg struct{}

var errTerminalInputClosed = errors.New("terminal input reached EOF")

type terminalModel struct {
	managedPolicyFailure      error
	spinnerTicker             *terminalSpinnerTicker
	teammateSpinnerWatches    map[string]*terminalTeammateSpinnerWatch
	state                     *State
	session                   Session
	ctx                       context.Context
	program                   *tea.Program
	options                   Options
	lifetime                  sync.Mutex
	closed                    bool
	worktreeChecked           bool
	inputEnded                bool
	todoPollUntil             time.Time
	localPanelPollUntil       time.Time
	terminalUpdateUntil       time.Time
	terminalPRFooter          *terminalPRFooterRuntime
	pluginInstallationChanges chan json.RawMessage
	statusLineChanges         chan json.RawMessage
	taskListChanges           chan json.RawMessage
	fullscreenChanges         chan struct{}
	expandedView              string
	cachedTeamsChanges        chan []protocol.JSONObject
	cachedTeamsFrames         chan terminalCachedTeamsFrameChangedMsg
	statusLineMountKnown      bool
	statusLineMounted         bool
	statusLineVim             string
	teamsMountID              string
	shortcutHelp              *terminalShortcutHelpRuntime
	turns                     sync.WaitGroup
	activeTurns               int
	turnsDrained              chan struct{}
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
	m.syncTerminalStatusLineMount()
	m.terminalUpdateUntil = time.Now().Add(30 * time.Minute)
	commands := []tea.Cmd{m.tick(), tea.RequestBackgroundColor, m.terminalUpdateCommand(), m.terminalPRFooterRestart(time.Now()), m.terminalRemoteStartCommand(), m.terminalRemoteTasksStartCommand(), m.terminalPluginInstallationCommand(), m.terminalStatusLineCommand(), m.terminalCachedTeamsCommand(), m.terminalFullscreenCommand(), m.terminalTaskListCommand(), m.updateTerminalStatusLine()}
	if m.options.InitialDraft != "" {
		m.state.Editor.Set(m.options.InitialDraft)
		m.state.syncWorkflowKeyword(time.Now())
	}
	if m.options.InitialPrompt != "" {
		m.state.Editor.Set(m.options.InitialPrompt)
		m.state.syncWorkflowKeyword(time.Now())
		commands = append(commands, m.actions(m.state.submit()))
		// Source's initial-message onSubmit has a no-op clearBuffer. Early
		// input stays editable while the positional prompt runs separately.
		m.state.Editor.Set(m.options.InitialDraft)
		m.state.syncWorkflowKeyword(time.Now())
	}
	commands = append(commands, m.syncTerminalTeammateSpinnerClock())
	return tea.Batch(commands...)
}

func (m *terminalModel) tick() tea.Cmd {
	interval := 200 * time.Millisecond
	if p := m.state.LocalPanel; p != nil && p.props.Kind == "btw" {
		value := presentationObject(p.props.Presentation)
		if value.String("response") == "" && value.String("error") == "" {
			interval = 80 * time.Millisecond
		}
	}
	return func() tea.Msg {
		timer := time.NewTimer(interval)
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
		case actionTerminalTeams:
			commands = append(commands, m.terminalTeamsCommand())
		case actionMessageSelector:
			commands = append(commands, m.messageSelectorCommand())
		case actionMessageSelection:
			commands = append(commands, m.messageSelectionCommand(a.messageSelection))
		case actionSubmit:
			commands = append(commands, func() tea.Msg {
				// Commands can be dispatched asynchronously while Quit is being
				// processed. Never start a Session turn after terminal teardown.
				if !m.beginTurn() {
					return returnedMsg{a.input.TurnID, context.Canceled}
				}
				defer m.endTurn()
				err := m.session.Submit(m.ctx, a.input, func(event Event) error {
					if event.TurnID == "" && event.Kind != PermissionAsked && event.Kind != PermissionUpdated && event.Kind != PermissionClosed {
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
			if a.localCommandToken == 0 {
				a.localCommandToken = m.state.beginTerminalLocalCommand(a.name)
			}
			if a.name == "chat:imagePaste" {
				commands = append(commands, m.clipboardImageCommand())
				continue
			}
			commands = append(commands, func() tea.Msg {
				if !m.beginTurn() {
					if a.localCommandToken != 0 {
						return localCommandReturnedMsg{a.localCommandToken, context.Canceled}
					}
					return actionReturnedMsg{context.Canceled}
				}
				defer m.endTurn()
				host, ok := m.session.(ActionSession)
				if !ok {
					if a.localCommandToken != 0 {
						return localCommandReturnedMsg{a.localCommandToken, &UnsupportedAction{Action: a.name}}
					}
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
				if a.localCommandToken != 0 {
					return localCommandReturnedMsg{a.localCommandToken, err}
				}
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
		case actionIDEPicker:
			commands = append(commands, m.idePickerCommand(a))
		case actionPluginHint:
			commands = append(commands, m.pluginHintCommand(a.name))
		case actionTeammate:
			commands = append(commands, m.teammateCommand(a.teammate))
		case actionLocalPanel:
			commands = append(commands, m.localPanelCommand(a.panel))
		}
	}
	commands = append(commands, m.terminalPRFooterSync(time.Now()))
	commands = append(commands, m.terminalShortcutHelpCommand())
	return tea.Batch(commands...)
}

func (m *terminalModel) Update(msg tea.Msg) (model tea.Model, command tea.Cmd) {
	defer func() { command = tea.Batch(command, m.syncTerminalSpinnerAnimation()) }()
	defer func() { command = tea.Batch(command, m.syncTerminalTeammateSpinnerClock()) }()
	defer func() { command = tea.Batch(command, m.syncTerminalExpandedView()) }()
	defer m.syncTerminalTeamsMount()
	defer m.publishTerminalLocalUI()
	defer m.syncTerminalStatusLineMount()
	switch msg := msg.(type) {
	case terminalTeammateSpinnerTickMsg:
		return m, m.terminalTeammateSpinnerTick(msg)
	case terminalSpinnerAnimationMsg:
		if msg.revision != 0 && (m.spinnerTicker == nil || !m.spinnerTicker.running || msg.revision != m.spinnerTicker.revision) {
			return m, nil
		}
		m.state.advanceTerminalSpinnerAnimation(msg)
		return m, m.terminalSpinnerAnimationCommand()
	case tea.FocusMsg:
		return m, m.terminalSpinnerFocus(true)
	case tea.BlurMsg:
		return m, m.terminalSpinnerFocus(false)
	case terminalExpandedViewReturnedMsg:
		if msg.err != nil && m.ctx.Err() == nil {
			m.state.Status = msg.err.Error()
		}
	case terminalFullscreenChangedMsg:
		if msg.err == nil {
			m.applyTerminalFullscreen(msg.enabled)
		} else if m.ctx.Err() == nil {
			m.state.Apply(Event{Kind: Notice, Text: msg.err.Error()})
		}
		return m, m.terminalFullscreenCommand()
	case terminalCachedTeamsFrameChangedMsg:
		m.state.applyTerminalCachedTeams(msg.teams)
		m.state.CachedTeamsFrame = msg.frame
		return m, m.terminalCachedTeamsCommand()
	case terminalShortcutHelpResult:
		if m.shortcutHelp != nil && m.shortcutHelp.mounted && m.shortcutHelp.revision == msg.revision {
			if msg.err != nil {
				m.state.Apply(Event{Kind: Notice, Text: msg.err.Error()})
			} else {
				m.state.ShortcutHelp = msg.value
			}
		}
	case terminalCachedTeamsChangedMsg:
		m.state.applyTerminalCachedTeams(msg.teams)
		return m, m.terminalCachedTeamsCommand()
	case terminalPaneTeamChangedMsg:
		m.state.paneTeam = bool(msg)
	case terminalPRFooterResultMsg:
		return m, m.terminalPRFooterResult(msg)
	case clipboardTextDoneMsg:
		return m, m.applyClipboardText(msg)
	case tea.BackgroundColorMsg:
		m.state.LightBackground = !msg.IsDark()
		return m, nil
	case clipboardImageMsg:
		m.applyClipboardImage(msg)
	case ideOnboardingChangedMsg:
		if msg.props == nil {
			m.state.IDEOnboarding = nil
		} else {
			m.state.OpenIDEPicker(*msg.props)
		}
	case pluginHintChangedMsg:
		m.state.PluginHint = nil
		if msg.hint != nil {
			m.state.PluginHint = &pluginHintState{hint: *msg.hint}
		}
	case pluginHintResponseMsg:
		if msg.err != nil {
			m.state.Status = msg.err.Error()
		} else if msg.notice != "" {
			m.state.applyNotice(Event{Kind: Notice, Text: msg.notice, NoticeTimeout: 5 * time.Second})
		}
	case messageSelectorOpenedMsg:
		if msg.err != nil {
			m.state.Status = msg.err.Error()
		} else {
			m.state.OpenMessageSelector(msg.snapshot)
		}
	case messageSelectionReturnedMsg:
		if msg.result.PermissionMode != "" {
			m.state.PermissionMode = msg.result.PermissionMode
		}
		if msg.result.History != nil {
			m.state.SetInitialHistory(msg.result.History, m.options.Fullscreen)
		}
		if msg.result.Mode != "" {
			m.state.Editor.Set(msg.result.Prefill)
			m.state.Mode = msg.result.Mode
			m.state.PastedContents = clonePastes(msg.result.PastedContents)
			for id := range msg.result.PastedContents {
				m.state.pasteSequence = max(m.state.pasteSequence, id)
			}
		}
		if msg.err != nil {
			if p := m.state.MessageSelector; p != nil {
				p.busy = false
				p.error = msg.err.Error()
			} else {
				m.state.Status = msg.err.Error()
			}
		} else {
			m.state.MessageSelector = nil
			m.state.Status = msg.result.Notice
		}

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
		var catalogue tea.Cmd
		if panel := m.state.LocalPanel; panel != nil && panel.props.MCPCatalog {
			catalogue = m.actions([]action{{kind: actionLocalPanel, panel: LocalPanelResult{ID: panel.props.ID, Action: "mcp-catalog-refresh"}}})
		}
		if source, ok := m.session.(WorkflowKeywordSource); ok {
			return m, tea.Batch(catalogue, func() tea.Msg {
				enabled, err := source.WorkflowKeywordEnabled(m.ctx)
				return workflowKeywordEnabledMsg{enabled, err}
			})
		}
		return m, catalogue
	case pluginInstallationChangedMsg:
		m.state.applyTerminalPluginInstallationStatus(msg.status, time.Now())
		return m, tea.Batch(m.actions(m.pluginInstallationChanged()), m.terminalPluginInstallationCommand())
	case terminalTaskListChangedMsg:
		m.state.applyTerminalTaskList(msg.value)
		return m, m.terminalTaskListCommand()
	case terminalStatusLineChangedMsg:
		m.state.applyTerminalStatusLine(msg.value)
		return m, m.terminalStatusLineCommand()
	case terminalStatusLineUpdateMsg:
		if msg.err != nil && m.ctx.Err() == nil {
			m.state.Status = msg.err.Error()
		}
	case keysChangedMsg:
		m.state.SetKeybindings([]Keybinding(msg))
		if source, ok := m.session.(KeybindingWarningSource); ok {
			m.state.SetKeybindingWarning(source.KeybindingWarningsSummary(), time.Now())
		}
	case workflowKeywordEnabledMsg:
		if msg.err != nil {
			m.state.Status = msg.err.Error()
		} else {
			m.state.SetWorkflowKeywordEnabled(msg.enabled, time.Now())
		}
	case tea.KeyPressMsg:
		defer func() { command = tea.Batch(command, m.terminalSpinnerFocus(true)) }()
		m.terminalPRFooterInteraction()
		if m.state.WorktreeExit != nil {
			return m, m.handleWorktreeKey(terminalKey(msg))
		}
		return m, tea.Batch(m.actions(m.state.HandleKey(terminalKey(msg), time.Now())), m.updateTerminalStatusLine())
	case tea.PasteStartMsg:
		m.state.Pasting = true
		return m, nil
	case tea.PasteEndMsg:
		m.state.Pasting = false
		return m, nil
	case tea.PasteMsg:
		m.terminalPRFooterInteraction()
		m.state.Paste(msg.Content)
		m.state.pluginHintInputChanged(time.Now())
		return m, m.terminalShortcutHelpCommand()
	case tea.WindowSizeMsg:
		m.state.Resize(msg.Width, msg.Height)
		m.state.commitTerminalSpinnerParent()
		if m.options.OnResize != nil {
			m.options.OnResize(m.state.Width, m.state.Height)
		}
	case eventMsg:
		if msg.event.Kind == TerminalRemoteStateChanged {
			close(msg.ack)
			return m, m.actions(m.state.applyTerminalRemoteState(msg.event))
		}
		if msg.event.Kind == FileEditorRequested {
			if msg.ack != nil {
				close(msg.ack)
			}
			return m, m.terminalFileEditorCommand(msg.event)
		}
		if msg.event.Kind == ClipboardTextRequested {
			close(msg.ack)
			return m, m.clipboardTextCommand(msg.event)
		}
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
		m.publishTerminalLocalUI()
		close(msg.ack)
		return m, tea.Batch(m.terminalPRFooterSync(time.Now()), m.updateTerminalStatusLine(), m.actions(m.state.nextTerminalQueuedInput()))
	case returnedMsg:
		return m, tea.Batch(m.actions(m.state.FinishTurn(msg.id, msg.err)), m.updateTerminalStatusLine())
	case permissionReturnedMsg:
		if errors.Is(msg.err, ErrManagedPolicyConsentDeclined) {
			m.managedPolicyFailure = msg.err
			return m, tea.Quit
		}
		if errors.Is(msg.err, ErrStartupConsentDeclined) {
			return m, m.actions(m.state.EOF())
		}
		if msg.err != nil {
			m.state.Status = msg.err.Error()
		}
	case actionEventMsg:
		if msg.event.Kind == TerminalRemoteStateChanged {
			if msg.ack != nil {
				close(msg.ack)
			}
			return m, m.actions(m.state.applyTerminalRemoteState(msg.event))
		}
		previousUpdateChannel := m.state.Preferences.UpdateChannel
		if msg.event.Kind == FileEditorRequested {
			if msg.ack != nil {
				close(msg.ack)
			}
			return m, m.terminalFileEditorCommand(msg.event)
		}
		if msg.event.Kind == ClipboardTextRequested {
			if msg.ack != nil {
				close(msg.ack)
			}
			return m, m.clipboardTextCommand(msg.event)
		}
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
		m.publishTerminalLocalUI()
		if msg.ack != nil {
			close(msg.ack)
		}
		if msg.event.Kind == TerminalPreferencesChanged && previousUpdateChannel != m.state.Preferences.UpdateChannel {
			return m, tea.Batch(m.terminalUpdateCommand(), m.terminalPRFooterSync(time.Now()), m.updateTerminalStatusLine())
		}
		return m, tea.Batch(m.terminalPRFooterSync(time.Now()), m.updateTerminalStatusLine(), m.actions(m.state.nextTerminalQueuedInput()))
	case localCommandReturnedMsg:
		return m, m.actions(m.state.finishTerminalLocalCommand(msg.token, msg.err))
	case actionReturnedMsg:
		if msg.err != nil {
			m.state.Status = msg.err.Error()
		}
	case fileEditorReturnedMsg:
		m.finishTerminalFileEditor(msg)
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
		var taskRefresh tea.Cmd
		if m.taskListChanges == nil && m.state.Teammates != nil && m.state.Teammates.ExpandedView == "tasks" && !time.Time(msg).Before(m.todoPollUntil) {
			m.todoPollUntil = time.Time(msg).Add(time.Second)
			taskRefresh = m.actions([]action{{kind: actionInvoke, name: "app:refreshTodos"}})
		}
		var panelRefresh tea.Cmd
		if panel := m.state.LocalPanel; panel != nil && (panel.props.Kind == "tasks" || panel.props.Kind == "workflows") && !time.Time(msg).Before(m.localPanelPollUntil) {
			m.localPanelPollUntil = time.Time(msg).Add(time.Second)
			panelRefresh = m.actions([]action{{kind: actionInvoke, name: "app:refreshLocalPanel"}})
		}
		return m, tea.Batch(m.tick(), m.terminalUpdateTick(time.Time(msg)), m.terminalPRFooterTick(time.Time(msg)), taskRefresh, panelRefresh, m.actions(m.state.pluginHintTick(time.Time(msg))), m.actions(m.state.ideOnboardingTick()))
	}
	return m, nil
}

func (m *terminalModel) View() tea.View {
	view := m.state.View(m.options.Title, m.options.Fullscreen)
	view.WindowTitle = m.options.TerminalTitle
	return view
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
		if model != nil {
			model.closeTerminalTeammateSpinnerClocks()
		}
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
	model.state.fullscreen = options.Fullscreen
	if source, ok := session.(TerminalExpandedViewSource); ok {
		initial, failure := source.TerminalExpandedView(ctx)
		if failure != nil {
			return failure
		}
		model.state.setTerminalExpandedView(initial)
	}
	model.expandedView = model.state.terminalExpandedView()
	if source, ok := session.(TerminalTaskListSource); ok {
		model.taskListChanges = make(chan json.RawMessage, 1)
		initial, remove, failure := source.SubscribeTerminalTaskList(ctx, func(value json.RawMessage) { enqueueTerminalStatusLine(ctx, model.taskListChanges, value) })
		if failure != nil {
			return failure
		}
		if remove != nil {
			defer remove()
		}
		model.state.applyTerminalTaskList(initial)
		if command := model.syncTerminalExpandedView(); command != nil {
			if completed, ok := command().(terminalExpandedViewReturnedMsg); ok && completed.err != nil {
				return completed.err
			}
		}
	}
	if source, ok := session.(TerminalFullscreenSource); ok {
		model.fullscreenChanges = make(chan struct{}, 1)
		initial, remove, failure := source.SubscribeTerminalFullscreen(ctx, func() {
			enqueueTerminalFullscreen(ctx, model.fullscreenChanges)
		})
		if failure != nil {
			return failure
		}
		if remove != nil {
			defer remove()
		}
		model.applyTerminalFullscreen(initial)
		options.Fullscreen = initial
	}
	model.state.statusLineHyperlinks = terminalStatusLineHyperlinkSupport(options)
	if options.InitialRemoteState != nil {
		initial := *options.InitialRemoteState
		model.state.RemoteState = &initial
	}
	if source, ok := session.(TerminalPresentationSource); ok {
		presentation, e := source.TerminalPresentation(ctx)
		if e != nil {
			return e
		}
		model.state.Apply(Event{Kind: SessionPresentationChanged, Result: presentation})
	}
	if source, ok := session.(TerminalBuddySource); ok {
		presentation, e := source.TerminalBuddySnapshot(ctx)
		if e != nil {
			return e
		}
		model.state.Apply(Event{Kind: TerminalBuddyChanged, Result: presentation})
	}
	if source, ok := session.(TerminalShortcutHelpSource); ok {
		help, e := source.TerminalShortcutHelp(ctx)
		if e != nil {
			return e
		}
		model.state.ShortcutHelp = help
	}
	if source, ok := session.(TerminalPreferenceSource); ok {
		preferences, e := source.TerminalPreferences(ctx)
		if e != nil {
			return e
		}
		model.state.SetTerminalPreferences(preferences)
	}
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
	if source, ok := session.(KeybindingSource); ok && options.Keybindings == nil {
		initial, remove, err := source.SubscribeKeybindings(ctx, func(bindings []Keybinding) { program.Send(keysChangedMsg(bindings)) })
		if err != nil {
			return err
		}
		if remove != nil {
			defer remove()
		}
		model.state.SetKeybindings(initial)
		if warnings, ok := session.(KeybindingWarningSource); ok {
			model.state.SetKeybindingWarning(warnings.KeybindingWarningsSummary(), time.Now())
		}
	}
	if source, ok := session.(TerminalPluginInstallationSource); ok {
		model.pluginInstallationChanges = make(chan json.RawMessage, 1)
		initial, remove, err := source.SubscribeTerminalPluginInstallation(ctx, func(status json.RawMessage) {
			enqueueTerminalPluginInstallation(ctx, model.pluginInstallationChanges, status)
		})
		if err != nil {
			return err
		}
		if remove != nil {
			defer remove()
		}
		model.state.applyTerminalPluginInstallationStatus(initial, time.Now())
	}
	if source, ok := session.(TerminalStatusLineSource); ok {
		model.statusLineChanges = make(chan json.RawMessage, 1)
		var initial json.RawMessage
		var remove func()
		var err error
		listener := func(value json.RawMessage) { enqueueTerminalStatusLine(ctx, model.statusLineChanges, value) }
		if mountedSource, ok := session.(TerminalStatusLineMountSource); ok {
			mounted, vim := model.state.terminalStatusLineInputMounted(options.Fullscreen), model.state.VimMode()
			initial, remove, err = mountedSource.SubscribeTerminalStatusLineMount(ctx, mounted, vim, listener)
			model.statusLineMountKnown, model.statusLineMounted, model.statusLineVim = true, mounted, vim
		} else {
			initial, remove, err = source.SubscribeTerminalStatusLine(ctx, listener)
		}
		if err != nil {
			return err
		}
		if remove != nil {
			defer remove()
		}
		model.state.applyTerminalStatusLine(initial)
	}
	if source, ok := session.(TerminalAutoConsentSource); ok {
		initial, remove, err := source.BindTerminalAutoConsent(ctx, func(event Event) error { program.Send(actionEventMsg{event: event}); return nil })
		if err != nil {
			return err
		}
		if remove != nil {
			defer remove()
		}
		model.state.applyInitialAutoConsent(initial)
	}
	if remove, e := model.bindPluginHints(); e != nil {
		return e
	} else if remove != nil {
		defer remove()
	}
	if source, ok := session.(IDEOnboardingSource); ok {
		initial, remove, e := source.SubscribeIDEOnboarding(ctx, func(props *IDEPicker) { program.Send(ideOnboardingChangedMsg{props}) })
		if e != nil {
			return e
		}
		if remove != nil {
			defer remove()
		}
		if initial != nil {
			model.state.OpenIDEPicker(*initial)
		}
	}
	if remove, e := model.bindTeammates(); e != nil {
		return e
	} else if remove != nil {
		defer remove()
	}
	if source, ok := session.(TerminalTeamFooterSource); ok {
		initial, unsubscribe, e := source.SubscribeTerminalTeamFooter(ctx, func(value bool) { program.Send(terminalPaneTeamChangedMsg(value)) })
		if e != nil {
			return e
		}
		if unsubscribe != nil {
			defer unsubscribe()
		}
		model.state.paneTeam = initial
	}
	if source, ok := session.(TerminalCachedTeamsFrameSource); ok {
		model.cachedTeamsFrames = make(chan terminalCachedTeamsFrameChangedMsg, 1)
		initial, remove, e := source.SubscribeTerminalCachedTeamsFrames(ctx, func(frame TerminalCachedTeamsFrame) {
			enqueueTerminalCachedTeamsFrame(ctx, model.cachedTeamsFrames, frame)
		})
		if e != nil {
			return e
		}
		if remove != nil {
			defer remove()
		}
		if initial != nil {
			model.state.applyTerminalCachedTeams(initial.InitialTeams())
		}
		model.state.CachedTeamsFrame = initial
	} else if source, ok := session.(TerminalCachedTeamsSource); ok {
		model.cachedTeamsChanges = make(chan []protocol.JSONObject, 1)
		initial, remove, e := source.SubscribeTerminalCachedTeams(ctx, func(teams []protocol.JSONObject) {
			enqueueTerminalCachedTeams(ctx, model.cachedTeamsChanges, teams)
		})
		if e != nil {
			return e
		}
		if remove != nil {
			defer remove()
		}
		model.state.applyTerminalCachedTeams(initial)
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
	// Program.Send blocks before Run. Subscribe only after the preceding
	// Owner-backed mounts, so an actual remote consent cannot pin that Owner
	// while a later mount waits for it. No business callback is run by this bind.
	if source, ok := session.(ManagedPolicyConsentSource); ok {
		initial, remove, err := source.BindManagedPolicyConsent(ctx, func(event Event) error { program.Send(actionEventMsg{event: event}); return nil })
		if err != nil {
			return err
		}
		if remove != nil {
			defer remove()
		}
		for _, request := range initial {
			model.state.Apply(Event{Kind: PermissionAsked, Permission: request})
		}
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
	if model.managedPolicyFailure != nil {
		err = errors.Join(err, model.managedPolicyFailure)
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
	switch msg.Code {
	case tea.KeyPgUp, tea.KeyKpPgUp:
		key.Name = strings.ReplaceAll(key.Name, "pgup", "pageup")
	case tea.KeyPgDown, tea.KeyKpPgDown:
		key.Name = strings.ReplaceAll(key.Name, "pgdown", "pagedown")
	}
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
