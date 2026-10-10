package cliauth

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/rivo/uniseg"
	"golang.org/x/text/unicode/norm"
	"io"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
)

type SetupTokenTerminal struct {
	Input        io.Reader
	Output       io.Writer
	Environment  []string
	AuthWarning  bool
	ForcedMethod string
	Clipboard    func(context.Context, string) string
}
type setupReadyMsg struct{ event SetupTokenEvent }
type setupReturnMsg struct{ err error }
type setupPasteReadyMsg struct{}
type setupCopiedMsg struct {
	raw   string
	epoch uint64
}
type setupCopyResetMsg struct{ epoch uint64 }
type setupModel struct {
	terminal       SetupTokenTerminal
	runtime        SetupTokenRuntime
	ctx            context.Context
	cancel         context.CancelFunc
	program        *tea.Program
	manual         chan string
	retry          chan struct{}
	workerDone     chan struct{}
	event          SetupTokenEvent
	showPaste      bool
	editor         tui.Editor
	copied         bool
	copyEpoch      uint64
	columns        int
	exitCode       int
	result         error
	commandMu      sync.Mutex
	commandWorkers sync.WaitGroup
	closing        bool
}

func (m *setupModel) Init() tea.Cmd { return nil }

func (m *setupModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch v := message.(type) {
	case setupReadyMsg:
		m.event = v.event
		if v.event.State == "waiting_for_login" {
			return m, m.timer(3*time.Second, setupPasteReadyMsg{})
		}
	case setupPasteReadyMsg:
		m.showPaste = true
	case setupCopiedMsg:
		if v.epoch != m.copyEpoch {
			return m, nil
		}
		if v.raw != "" {
			_, _ = io.WriteString(m.terminal.Output, v.raw)
		}
		m.copied = true
		return m, m.timer(2*time.Second, setupCopyResetMsg{v.epoch})
	case setupCopyResetMsg:
		if v.epoch == m.copyEpoch {
			m.copied = false
		}
	case tea.WindowSizeMsg:
		m.columns = v.Width
	case setupReturnMsg:
		m.result = v.err
		return m, tea.Quit
	case tea.KeyPressMsg:
		switch v.String() {
		case "ctrl+c":
			// setup-token uses getBaseRenderOptions(false). Raw Ctrl-C is
			// TextInput's clear operation, not an application exit key.
			if m.showPaste && m.event.State == "waiting_for_login" {
				m.editor.Reset()
			}
			return m, nil
		case "enter":
			if m.event.State == "error" {
				m.editor.Reset()
				select {
				case m.retry <- struct{}{}:
				default:
				}
				return m, nil
			}
			if m.event.State == "waiting_for_login" && m.showPaste {
				select {
				case m.manual <- m.editor.Text():
				default:
				}
			}
		default:
			if m.showPaste && m.event.State == "waiting_for_login" {
				m.edit(v)
			}
		}
	case tea.PasteMsg:
		if m.showPaste && m.event.State == "waiting_for_login" {
			m.editor.Insert(norm.NFC.String(strings.ReplaceAll(ansi.Strip(v.Content), "\r", "\n")))
		}
	}
	if m.event.State == "waiting_for_login" && m.showPaste && !m.copied && m.editor.Text() == "c" && m.terminal.Clipboard != nil {
		m.editor.Reset()
		m.copyEpoch++
		epoch, url := m.copyEpoch, m.event.URL
		return m, m.command(func() tea.Msg { return setupCopiedMsg{m.terminal.Clipboard(m.ctx, url), epoch} })
	}
	return m, nil
}
func (m *setupModel) View() tea.View {
	var text strings.Builder
	text.WriteString("Claude Code\n\n")
	if m.terminal.AuthWarning {
		text.WriteString("Warning: You already have authentication configured via environment variable or API key helper.\nThe setup-token command will create a new OAuth token which you can use instead.\n\n")
	}
	switch m.event.State {
	case "success":
		text.WriteString(SetupTokenSuccessText(m.event.Token))
	case "about_to_retry":
		text.WriteString("Retrying…\n")
	case "error":
		text.WriteString("OAuth error: " + m.event.Message + "\n\nPress Enter to retry.\n")
	default:
		if m.terminal.ForcedMethod == "claudeai" {
			text.WriteString("Login method pre-selected: Subscription Plan (Claude Pro/Max)\n")
		}
		if m.terminal.ForcedMethod == "console" {
			text.WriteString("Login method pre-selected: API Usage Billing (Anthropic Console)\n")
		}
		if !m.showPaste {
			text.WriteString("Opening browser to sign in…\n")
		} else {
			hint := "(c to copy)"
			if m.copied {
				hint = "(Copied!)"
			}
			text.WriteString("Browser didn't open? Use the url below to sign in " + hint + "\n" + m.event.URL + "\n\nPaste code here if prompted > " + setupTokenMasked(m.editor.Text(), m.columns-len("Paste code here if prompted > ")-2) + "\n")
		}
	}
	return tea.NewView(text.String())
}
func RunSetupTokenTerminal(ctx context.Context, runtime SetupTokenRuntime, terminal SetupTokenTerminal) (result Result, err error) {
	// Bubble Tea ignores native restore errors. Keep the caller's actual TTY
	// configuration and return restoration failures after its renderer joins.
	type saved struct {
		file  term.File
		state *term.State
	}
	var terminals []saved
	for _, stream := range []any{terminal.Input, terminal.Output} {
		if file, ok := stream.(term.File); ok && term.IsTerminal(file.Fd()) {
			state, e := term.GetState(file.Fd())
			if e != nil {
				return Result{}, e
			}
			terminals = append(terminals, saved{file, state})
		}
	}
	defer func() {
		for _, terminal := range terminals {
			err = errors.Join(err, term.Restore(terminal.file.Fd(), terminal.state))
		}
	}()

	if terminal.Input == nil || terminal.Output == nil {
		return Result{}, io.ErrUnexpectedEOF
	}
	lifetime, cancel := context.WithCancel(ctx)
	defer cancel()
	m := &setupModel{ctx: lifetime, cancel: cancel, runtime: runtime, terminal: terminal, manual: make(chan string, 1), retry: make(chan struct{}, 1), columns: 80}
	m.runtime.Manual = m.manual
	m.runtime.Retry = m.retry
	program := tea.NewProgram(m, tea.WithContext(context.WithoutCancel(ctx)), tea.WithoutSignalHandler(), tea.WithInput(terminal.Input), tea.WithOutput(terminal.Output), tea.WithEnvironment(terminal.Environment), tea.WithWindowSize(80, 24))
	m.program = program
	m.runtime.Event = func(event SetupTokenEvent) error { program.Send(setupReadyMsg{event}); return nil }
	m.workerDone = make(chan struct{})
	go func() { defer close(m.workerDone); program.Send(setupReturnMsg{SetupToken(m.ctx, m.runtime)}) }()
	_, e := program.Run()
	m.commandMu.Lock()
	m.closing = true
	m.commandMu.Unlock()
	cancel()
	m.commandWorkers.Wait()
	<-m.workerDone
	if e != nil {
		return Result{}, e
	}
	if m.exitCode == 130 {
		return Result{ExitCode: 130}, nil
	}
	if ctx.Err() != nil {
		return Result{ExitCode: 130}, nil
	}
	if m.result != nil && !errors.Is(m.result, context.Canceled) {
		return Result{}, m.result
	}
	return Result{}, nil
}

// TextInput cursor operations share the existing grapheme editor. OAuth pastes
// use TextInput normalization, rather than PromptInput's tab-expanding paste.
func (m *setupModel) edit(k tea.KeyPressMsg) {
	normalized := norm.NFC.String(m.editor.Text())
	if normalized != m.editor.Text() {
		offset := len(norm.NFC.String(m.editor.Text()[:m.editor.Cursor()]))
		m.editor.Set(normalized)
		for m.editor.Cursor() > offset {
			m.editor.Left()
		}
	}

	switch k.String() {
	case "left", "ctrl+b":
		m.editor.Left()
	case "right", "ctrl+f":
		m.editor.Right()
	case "home", "ctrl+a", "pgup":
		m.editor.Home()
	case "end", "ctrl+e", "pgdown":
		m.editor.End()
	case "backspace", "ctrl+h":
		m.editor.Backspace()
	case "delete", "ctrl+d":
		m.editor.Delete()
	case "ctrl+u":
		m.editor.KillToStart()
	case "ctrl+k":
		m.editor.KillToEnd()
	case "ctrl+w", "alt+backspace", "ctrl+backspace":
		m.editor.KillWordBefore()
	case "alt+d":
		m.editor.KillWordAfter()
	case "alt+b", "ctrl+left", "alt+left":
		m.editor.WordLeft()
	case "alt+f", "ctrl+right", "alt+right":
		m.editor.WordRight()
	case "ctrl+y":
		m.editor.Yank()
	case "alt+y":
		m.editor.YankPop()
	case "up", "ctrl+p":
		m.editor.Vertical(-1)
	case "down", "ctrl+n":
		m.editor.Vertical(1)
	case "tab", "esc":
	default:
		m.editor.Insert(norm.NFC.String(strings.ReplaceAll(ansi.Strip(k.Text), "\r", "\n")))
	}
}
func setupTokenMasked(text string, columns int) string {
	if columns < 1 {
		columns = 1
	}
	var lines [][]string
	line := []string{}
	width := 0
	g := uniseg.NewGraphemes(norm.NFC.String(text))
	for g.Next() {
		if g.Str() == "\n" {
			lines = append(lines, line)
			line = []string{}
			width = 0
			continue
		}
		if width+g.Width() > columns && len(line) > 0 {
			lines = append(lines, line)
			line = []string{}
			width = 0
		}
		line = append(line, g.Str())
		width += g.Width()
	}
	lines = append(lines, line)
	var result []string
	for i, graphemes := range lines {
		visible := 0
		if i == len(lines)-1 {
			visible = min(6, len(graphemes))
		}
		result = append(result, strings.Repeat("*", len(graphemes)-visible)+strings.Join(graphemes[len(graphemes)-visible:], ""))
	}
	return strings.Join(result, "\n")
}

func (m *setupModel) command(run tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		m.commandMu.Lock()
		if m.closing {
			m.commandMu.Unlock()
			return nil
		}
		m.commandWorkers.Add(1)
		m.commandMu.Unlock()
		defer m.commandWorkers.Done()
		return run()
	}
}
func (m *setupModel) timer(delay time.Duration, message tea.Msg) tea.Cmd {
	return m.command(func() tea.Msg {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
			return message
		case <-m.ctx.Done():
			return nil
		}
	})
}
