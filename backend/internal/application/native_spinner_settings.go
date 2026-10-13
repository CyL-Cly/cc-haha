package application

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (s *Session) emitNativeSpinner(ctx context.Context, turnID string, emit tui.Emit) error {
	if !s.terminalInteractive || emit == nil {
		return nil
	}
	verb, err := s.process.SpinnerVerb(ctx)
	if err != nil {
		return err
	}
	return emit(tui.Event{TurnID: turnID, Kind: tui.Notice, Text: verb + "…"})
}
