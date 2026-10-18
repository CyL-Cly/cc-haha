package application

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (s *Session) CheckTerminalUpdate(ctx context.Context, emit tui.Emit) error {
	return s.process.CheckTerminalUpdate(ctx, func(value protocol.JSONObject) error {
		if emit == nil {
			return nil
		}
		return emit(tui.Event{Kind: tui.TerminalUpdateChanged, Result: raw(value), Background: true})
	})
}
