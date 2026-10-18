package tui

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/NanmiCoder/cc-haha/backend/internal/terminalcapabilities"
	"github.com/charmbracelet/x/term"
)

type TerminalStatusLineSource interface {
	SubscribeTerminalStatusLine(context.Context, func(json.RawMessage)) (json.RawMessage, func(), error)
	UpdateTerminalStatusLine(context.Context, string) error
}

type TerminalStatusLineMountSource interface {
	SubscribeTerminalStatusLineMount(context.Context, bool, string, func(json.RawMessage)) (json.RawMessage, func(), error)
	SetTerminalStatusLineMounted(context.Context, bool, string) error
}

func (s *State) terminalStatusLineInputMounted(fullscreen bool) bool {
	if s.Closing || s.Editing || s.Pasting || s.HelpOpen || !fullscreen && len(s.Completions) > 0 || s.Mode != "prompt" || fullscreen && s.Height < 24 || s.Doctor != nil || s.DreamDialog != nil || s.MessageSelector != nil || s.WorktreeExit != nil || s.ThinkingPicker != nil || s.ModelPicker != nil || s.IDEPicker != nil || s.ideOnboardingVisible() || s.pluginHintVisible() || s.Transcript || len(s.Permissions) > 0 {
		return false
	}
	return s.LocalPanel == nil || s.LocalPanel.props.KeepPromptInput
}

func (m *terminalModel) syncTerminalStatusLineMount() {
	source, ok := m.session.(TerminalStatusLineMountSource)
	if !ok || m.ctx.Err() != nil {
		return
	}
	mounted, vim := m.state.terminalStatusLineInputMounted(m.options.Fullscreen), m.state.VimMode()
	if m.statusLineMountKnown && m.statusLineMounted == mounted && m.statusLineVim == vim {
		return
	}
	m.statusLineMountKnown, m.statusLineMounted, m.statusLineVim = true, mounted, vim
	if err := source.SetTerminalStatusLineMounted(m.ctx, mounted, vim); err != nil && m.ctx.Err() == nil {
		m.state.Status = err.Error()
	}
}

type terminalStatusLineChangedMsg struct{ value json.RawMessage }
type terminalStatusLineUpdateMsg struct{ err error }

func enqueueTerminalStatusLine(ctx context.Context, queue chan json.RawMessage, value json.RawMessage) {
	copy := append(json.RawMessage(nil), value...)
	select {
	case <-ctx.Done():
		return
	default:
	}
	select {
	case queue <- copy:
		return
	default:
	}
	select {
	case <-queue:
	default:
	}
	select {
	case queue <- copy:
	case <-ctx.Done():
	default:
	}
}

func (m *terminalModel) terminalStatusLineCommand() tea.Cmd {
	if m.statusLineChanges == nil {
		return nil
	}
	return func() tea.Msg {
		if !m.beginTerminalUpdate() {
			return nil
		}
		defer m.turns.Done()
		select {
		case <-m.ctx.Done():
			return nil
		case value := <-m.statusLineChanges:
			return terminalStatusLineChangedMsg{value}
		}
	}
}

func (m *terminalModel) updateTerminalStatusLine() tea.Cmd {
	source, ok := m.session.(TerminalStatusLineSource)
	if !ok {
		return nil
	}
	vim := m.state.VimMode()
	return func() tea.Msg {
		if !m.beginTerminalUpdate() {
			return nil
		}
		defer m.turns.Done()
		return terminalStatusLineUpdateMsg{source.UpdateTerminalStatusLine(m.ctx, vim)}
	}
}

func (s *State) applyTerminalStatusLine(value json.RawMessage) {
	s.TerminalStatusLine = presentationObject(append(json.RawMessage(nil), value...))
}

func terminalStatusLineHyperlinkSupport(options Options) bool {
	env := map[string]string{}
	for _, item := range options.Environment {
		if key, value, ok := strings.Cut(item, "="); ok {
			env[key] = value
		}
	}
	tty := false
	if output, ok := options.Output.(interface{ Fd() uintptr }); ok {
		tty = term.IsTerminal(output.Fd())
	}
	color := tty && env["NO_COLOR"] == "" && env["FORCE_COLOR"] != "0"
	return terminalcapabilities.SupportsHyperlinks(env, terminalcapabilities.LibraryHyperlinks(env, runtime.GOOS, tty, color, nil))
}

func (s *State) terminalStatusLineLines(fullscreen bool) []string {
	if string(s.TerminalStatusLine.Get("configured")) != "true" {
		return nil
	}
	text := s.TerminalStatusLine.String("text")
	if text == "" {
		if fullscreen {
			return []string{""}
		}
		return nil
	}
	padding := float64(0)
	_ = json.Unmarshal(s.TerminalStatusLine.Get("padding"), &padding)
	theme := s.previewTheme()
	if theme == "auto" {
		theme = "dark"
		if s.LightBackground {
			theme = "light"
		}
	}
	return TerminalStatusLineTextRowsForThemeNumberPadding(text, s.Width, padding, theme, s.statusLineHyperlinks)
}
