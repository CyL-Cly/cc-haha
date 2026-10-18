package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
)

func (m *terminalModel) localPanelCommand(result LocalPanelResult) tea.Cmd {
	return func() tea.Msg {
		if !m.beginTurn() {
			return actionReturnedMsg{context.Canceled}
		}
		defer m.endTurn()
		host, ok := m.session.(LocalPanelSession)
		if !ok {
			return actionReturnedMsg{&UnsupportedAction{Action: "panel:" + result.Action}}
		}
		err := host.ApplyLocalPanel(m.ctx, result, func(event Event) error {
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
	}
}
