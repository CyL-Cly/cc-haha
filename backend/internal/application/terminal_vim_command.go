package application

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func terminalVimCommands() []tui.Completion {
	return []tui.Completion{{Name: "vim", Description: "Toggle between Vim and Normal editing modes", Kind: "local"}}
}

func (s *Session) nativeEditorModeChanged(mode string) error {
	return s.emitEvent(tui.Event{Kind: tui.EventKind("editor_mode_changed"), Text: mode, Background: true})
}

func (s *Session) invokeTerminalVimCommand(ctx context.Context, name, args string, emit tui.Emit) (bool, error) {
	if name != "vim" || !s.terminalInteractive {
		return false, nil
	}
	text, err := s.process.ToggleTerminalVim(ctx)
	if err != nil {
		return true, err
	}
	if err = s.emitTerminalConfig(ctx, emit); err != nil {
		return true, err
	}
	if emit != nil {
		return true, emit(tui.Event{Kind: tui.LocalCommandOutput, Text: text, Background: true})
	}
	return true, nil
}
