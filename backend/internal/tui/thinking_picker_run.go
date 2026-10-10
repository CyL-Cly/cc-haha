package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
)

func (m *terminalModel) thinkingPickerCommand(a action) tea.Cmd {
	return func() tea.Msg {
		if !m.beginTurn() {
			return actionReturnedMsg{context.Canceled}
		}
		defer m.endTurn()
		host, ok := m.session.(ThinkingPickerSession)
		if !ok {
			return actionReturnedMsg{&UnsupportedAction{Action: "thinkingPicker:select"}}
		}
		e := host.RespondThinkingPicker(m.ctx, a.permission, a.thinkingPickerResult, func(event Event) error {
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
