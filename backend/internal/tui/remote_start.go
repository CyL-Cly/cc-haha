package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

type TerminalRemoteStartSession interface{ StartTerminalRemote(context.Context) error }

// Remote's mount effect starts after subscriptions and the program receive
// loop exist. Startup I/O uses the same tracked terminal lifetime as actions.
func (m *terminalModel) terminalRemoteStartCommand() tea.Cmd {
	host, ok := m.session.(TerminalRemoteStartSession)
	if !ok {
		return nil
	}
	return func() tea.Msg {
		if !m.beginTurn() {
			return nil
		}
		defer m.endTurn()
		return actionReturnedMsg{err: host.StartTerminalRemote(m.ctx)}
	}
}
