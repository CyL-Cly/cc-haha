package application

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"strings"
)

func (s *Session) submitTerminalBash(ctx context.Context, in tui.Input) error {
	result, err := s.process.RunTerminalBash(ctx, in.Text, func(_ json.RawMessage, progress protocol.JSONObject) error {
		return s.emitEvent(tui.Event{Kind: tui.BashProgress, Text: progress.String("output")})
	})
	if err != nil {
		return err
	}
	text := result.Stdout
	if result.Stderr != "" {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += result.Stderr
	}
	return s.emitEvent(tui.Event{Kind: tui.BashFinished, Text: text, IsError: result.Stderr != "", Cancelled: result.Interrupted})
}
