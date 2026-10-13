package tui

import (
	tea "charm.land/bubbletea/v2"
	"errors"
)

type messageSelectorOpenedMsg struct {
	snapshot MessageSelectorSnapshot
	err      error
}
type messageSelectionReturnedMsg struct {
	result MessageSelectionResult
	err    error
}

func (m *terminalModel) messageSelectorCommand() tea.Cmd {
	return func() tea.Msg {
		host, ok := m.session.(MessageSelectorSession)
		if !ok {
			return messageSelectorOpenedMsg{err: &UnsupportedAction{Action: "messageSelector:open"}}
		}
		snapshot, err := host.MessageSelectorSnapshot(m.ctx)
		return messageSelectorOpenedMsg{snapshot, err}
	}
}
func (m *terminalModel) messageSelectionCommand(selection MessageSelection) tea.Cmd {
	return func() tea.Msg {
		if !m.beginTurn() {
			return messageSelectionReturnedMsg{err: errors.New("terminal is closing")}
		}
		defer m.endTurn()
		host, ok := m.session.(MessageSelectorSession)
		if !ok {
			return messageSelectionReturnedMsg{err: &UnsupportedAction{Action: "messageSelector:select"}}
		}
		result, err := host.SelectMessage(m.ctx, selection, func(event Event) error {
			ack := make(chan struct{})
			m.program.Send(actionEventMsg{event, ack})
			select {
			case <-ack:
				return nil
			case <-m.ctx.Done():
				return m.ctx.Err()
			}
		})
		return messageSelectionReturnedMsg{result, err}
	}
}
