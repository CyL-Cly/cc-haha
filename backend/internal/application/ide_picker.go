package application

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"strings"
)

func (s *Session) InvokeIDEPicker(ctx context.Context, args string, emit tui.Emit) error {
	props, e := s.process.NativeIDECommand(ctx, args)
	if e != nil {
		return e
	}
	if strings.TrimSpace(args) == "open" && len(props.Options) == 0 {
		return emit(tui.Event{Kind: tui.Notice, Text: "No IDEs with Claude Code extension detected."})
	}
	if props.Mode == "install" && len(props.Options) == 1 {
		notice, e := s.process.ApplyNativeIDESelection(ctx, props.ID, tui.IDEPickerResult{Value: props.Options[0].Value})
		if e != nil {
			return e
		}
		return emit(tui.Event{Kind: tui.Notice, Text: notice})
	}
	return emit(tui.Event{Kind: tui.IDEPickerOpened, IDEPicker: &props})
}
func (s *Session) RespondIDEPicker(ctx context.Context, id string, result tui.IDEPickerResult, emit tui.Emit) error {
	notice, e := s.process.ApplyNativeIDESelection(ctx, id, result)
	if e != nil {
		return e
	}
	if notice != "" && emit != nil {
		return emit(tui.Event{Kind: tui.Notice, Text: notice})
	}
	return nil
}
