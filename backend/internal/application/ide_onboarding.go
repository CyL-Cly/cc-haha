package application

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (s *Session) SubscribeIDEOnboarding(ctx context.Context, listener func(*tui.IDEPicker)) (*tui.IDEPicker, func(), error) {
	if !s.terminalInteractive {
		return nil, func() {}, nil
	}
	return s.process.SubscribeIDEOnboarding(ctx, listener)
}
