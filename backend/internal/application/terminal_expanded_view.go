package application

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (s *Session) TerminalExpandedView(ctx context.Context) (string, error) {
	return s.process.TerminalExpandedView(ctx)
}

func (s *Session) SetTerminalExpandedView(ctx context.Context, value string) error {
	return s.process.SetTerminalExpandedView(ctx, value)
}

func (s *Session) EnqueueTerminalExpandedView(ctx context.Context, value string) (tui.TerminalExpandedViewCommit, error) {
	return s.process.EnqueueTerminalExpandedView(ctx, value)
}
