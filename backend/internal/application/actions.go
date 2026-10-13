package application

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

var _ tui.ActionSession = (*Session)(nil)

// Commands use the existing SDK input processor and shared session. Unknown
// host actions fail before a model request instead of becoming user text.
func (s *Session) InvokeAction(ctx context.Context, action string, emit tui.Emit) error {
	switch action {
	case "chat:cycleMode":
		mode, err := s.process.CycleTerminalPermissionMode(ctx)
		if err != nil {
			return err
		}
		if err = s.scheduleTerminalAutoConsent(ctx, emit); err != nil {
			return err
		}
		if emit != nil {
			return emit(tui.Event{Kind: tui.PermissionModeChanged, PermissionMode: mode})
		}
		return nil
	case "chat:thinkingToggle":
		return s.InvokeThinkingPicker(ctx, emit)
	case "task:background":
		_, err := s.process.BackgroundFromTerminal(ctx)
		return err
	case "command:cost":
		return s.Submit(ctx, tui.Input{TurnID: newID(), Text: "/cost", Mode: "prompt"}, emit)
	case "command:model", "chat:modelPicker":
		if emit == nil {
			emit = func(tui.Event) error { return nil }
		}
		return s.InvokeModelPicker(ctx, action == "command:model", emit)
	}
	return &tui.UnsupportedAction{Action: action}
}
