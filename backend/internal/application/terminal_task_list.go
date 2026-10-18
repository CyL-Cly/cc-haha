package application

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (s *Session) SubscribeTerminalTaskList(ctx context.Context, changed func(json.RawMessage)) (json.RawMessage, func(), error) {
	return s.process.SubscribeNativeTerminalTaskList(ctx, changed)
}

type terminalFooterTasksOrigin struct{}

func terminalTaskListCommandEmit(ctx context.Context, name string, emit tui.Emit) tui.Emit {
	if emit == nil {
		return nil
	}
	localJSX := false
	for _, command := range terminalPanelCommands() {
		if command.Name == name {
			localJSX = command.Kind == "local-jsx"
			break
		}
	}
	if !localJSX {
		return emit
	}
	if ctx.Value(terminalFooterTasksOrigin{}) == true {
		localJSX = false
	}
	return func(event tui.Event) error {
		if event.LocalPanel != nil && (event.Kind == tui.LocalPanelOpened || event.Kind == tui.LocalPanelRefreshed) {
			panel := *event.LocalPanel
			panel.OriginKnown, panel.LocalJSXCommand = true, localJSX
			event.LocalPanel = &panel
		}
		return emit(event)
	}
}
