package application

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (s *Session) completeTerminalLocalJSX(ctx context.Context, name, args string, output json.RawMessage, display string, emit tui.Emit) error {
	records, err := s.process.CompleteTerminalLocalJSXRecords(ctx, name, args, output, display, false)
	if err != nil || len(records) == 0 || emit == nil {
		return err
	}
	return s.localInputMessagesTo(records, func(event tui.Event) error {
		event.Background = true
		event.TurnID = records[0].String("uuid")
		return emit(event)
	})
}
