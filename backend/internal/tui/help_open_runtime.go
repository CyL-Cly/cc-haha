package tui

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"
)

type terminalShortcutHelpRuntime struct {
	mounted  bool
	revision uint64
}
type terminalShortcutHelpResult struct {
	revision uint64
	value    TerminalShortcutHelp
	err      error
}

func (m *terminalModel) terminalShortcutHelpCommand() tea.Cmd {
	if m.shortcutHelp == nil {
		m.shortcutHelp = &terminalShortcutHelpRuntime{}
	}
	runtime := m.shortcutHelp
	if runtime.mounted == m.state.HelpOpen {
		return nil
	}
	runtime.mounted = m.state.HelpOpen
	runtime.revision++
	if !runtime.mounted {
		return nil
	}
	source, ok := m.session.(TerminalShortcutHelpSource)
	if !ok {
		return nil
	}
	revision := runtime.revision
	return func() tea.Msg {
		if !m.beginTerminalUpdate() {
			return nil
		}
		defer m.turns.Done()
		value, err := source.TerminalShortcutHelp(m.ctx)
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return terminalShortcutHelpResult{revision: revision, value: value, err: err}
	}
}
