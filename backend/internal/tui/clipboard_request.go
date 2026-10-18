package tui

import (
	"bytes"
	tea "charm.land/bubbletea/v2"
	"context"
	"runtime"
)

type clipboardTextDoneMsg struct {
	sequence string
	err      error
	reply    chan error
}

func (m *terminalModel) clipboardTextCommand(event Event) tea.Cmd {
	return func() tea.Msg {
		if !m.beginTurn() {
			err := m.ctx.Err()
			if err == nil {
				err = context.Canceled
			}
			return clipboardTextDoneMsg{err: err, reply: event.ClipboardTextReply}
		}
		defer m.endTurn()
		var output bytes.Buffer
		err := WriteClipboardText(m.ctx, runtime.GOOS, m.options.Environment, event.Text, &output)
		return clipboardTextDoneMsg{sequence: output.String(), err: err, reply: event.ClipboardTextReply}
	}
}
func (m *terminalModel) applyClipboardText(msg clipboardTextDoneMsg) tea.Cmd {
	if msg.reply != nil {
		select {
		case msg.reply <- msg.err:
		case <-m.ctx.Done():
		}
	}
	if msg.err != nil {
		m.state.Status = msg.err.Error()
		return nil
	}
	return tea.Raw(msg.sequence)
}
