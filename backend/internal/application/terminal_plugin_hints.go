package application

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func terminalPluginHint(h *clibootstrap.NativePluginHint) *tui.PluginHint {
	if h == nil {
		return nil
	}
	return &tui.PluginHint{Name: h.Name, Marketplace: h.Marketplace, Description: h.Description, SourceCommand: h.SourceCommand}
}
func (s *Session) SubscribePluginHints(ctx context.Context, listener func(*tui.PluginHint)) (*tui.PluginHint, func(), error) {
	if !s.terminalInteractive {
		return nil, func() {}, nil
	}
	initial, unsubscribe, err := s.process.SubscribePluginHints(ctx, func(h *clibootstrap.NativePluginHint) {
		if listener != nil {
			listener(terminalPluginHint(h))
		}
	})
	return terminalPluginHint(initial), unsubscribe, err
}
func (s *Session) RespondPluginHint(ctx context.Context, response string) (string, error) {
	return s.process.RespondPluginHint(ctx, response)
}
