package application

import "github.com/NanmiCoder/cc-haha/backend/internal/tui"

func (s *Session) openTerminalMessageSelector(snapshot tui.MessageSelectorSnapshot) error {
	return s.emitEvent(tui.Event{Kind: tui.MessageSelectorOpened, MessageSelector: &snapshot})
}
