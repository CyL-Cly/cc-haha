package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
)

func (m *terminalModel) idePickerCommand(a action) tea.Cmd {
	return func() tea.Msg {
		if !m.beginTurn() {
			return actionReturnedMsg{context.Canceled}
		}
		defer m.endTurn()
		host, ok := m.session.(IDEPickerSession)
		if !ok {
			return actionReturnedMsg{&UnsupportedAction{Action: "idePicker:select"}}
		}
		e := host.RespondIDEPicker(m.ctx, a.permission, a.idePickerResult, func(event Event) error {
			ack := make(chan struct{})
			m.program.Send(actionEventMsg{event: event, ack: ack})
			select {
			case <-ack:
				return nil
			case <-m.ctx.Done():
				return m.ctx.Err()
			}
		})
		return actionReturnedMsg{e}
	}
}
