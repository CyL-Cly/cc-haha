package application

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (s *Session) TerminalShortcutHelp(ctx context.Context) (tui.TerminalShortcutHelp, error) {
	return s.process.TerminalShortcutHelp(ctx)
}
