package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

type TerminalRemoteTasksStartSession interface{ StartTerminalRemoteTasks(context.Context) error }

func (m *terminalModel) terminalRemoteTasksStartCommand() tea.Cmd {
	source, ok := m.session.(TerminalRemoteTasksStartSession)
	if !ok {
		return nil
	}
	return func() tea.Msg {
		if !m.beginTurn() {
			return nil
		}
		defer m.endTurn()
		return actionReturnedMsg{err: source.StartTerminalRemoteTasks(m.ctx)}
	}
}
