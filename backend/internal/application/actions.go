package application

import (
	"context"
	"fmt"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

var _ tui.ActionSession = (*Session)(nil)

// Commands use the existing SDK input processor and shared session. Unknown
// host actions fail before a model request instead of becoming user text.
func (s *Session) InvokeAction(ctx context.Context, action string, emit tui.Emit) error {
	if strings.HasPrefix(action, "command:panel:") {
		parts := strings.SplitN(strings.TrimPrefix(action, "command:panel:"), "\x00", 2)
		args := ""
		if len(parts) == 2 {
			args = parts[1]
		}
		if handled, err := s.InvokeTerminalCommand(ctx, parts[0], args, emit); handled {
			return err
		}
		return &tui.UnsupportedAction{Action: action}
	}
	if strings.HasPrefix(action, "command:") {
		name := strings.TrimPrefix(action, "command:")
		if name == "tasks" || name == "bashes" {
			// PromptInput's footer mounts BashesDialog directly. The slash
			// command uses local JSX; both reuse this actual task consumer.
			ctx = context.WithValue(ctx, terminalFooterTasksOrigin{}, true)
		}
		if handled, err := s.InvokeTerminalCommand(ctx, name, "", emit); handled {
			return err
		}
		if name == "doctor" {
			return s.InvokeDoctor(ctx, emit)
		}
		if name == "ide" {
			return s.InvokeIDEPicker(ctx, "", emit)
		}
		if name != "model" && name != "cost" {
			s.mu.Lock()
			available := false
			for _, command := range s.commandSnapshot {
				available = available || command.Name == name
			}
			s.mu.Unlock()
			if available {
				return s.Submit(ctx, tui.Input{TurnID: newID(), Text: "/" + name, Mode: "prompt"}, emit)
			}
		}
	}
	switch action {
	case "app:markTasksHint":
		return s.process.MarkTerminalTasksHint(ctx)
	case "app:refreshLocalPanel":
		return s.refreshActiveTerminalPanel(ctx, emit)
	case "app:refreshTodos":
		value, err := s.process.TerminalPanelSnapshot(ctx, "todos")
		if err != nil {
			return err
		}
		if emit != nil {
			return emit(tui.Event{Kind: tui.TaskItemsChanged, Result: value.Get("tasks")})
		}
		return nil
	case "chat:killAgents":
		count, err := s.process.StopTerminalAgents(ctx)
		if err != nil {
			return err
		}
		if emit != nil {
			return emit(tui.Event{Kind: tui.Notice, Text: fmt.Sprintf("Stopped %d background agent(s)", count)})
		}
		return nil
	case "chat:fastMode":
		value, err := s.process.TerminalFastMode(ctx, nil)
		if err != nil {
			return err
		}
		if value.String("unavailableReason") != "" {
			return nil
		}
		state := &terminalLocalPanelState{props: tui.LocalPanel{ID: newID(), Kind: "fast"}}
		if err = s.refreshTerminalPanel(ctx, state); err != nil {
			return err
		}
		s.mu.Lock()
		s.terminalPanel = state
		s.mu.Unlock()
		if emit != nil {
			return emit(tui.Event{Kind: tui.LocalPanelOpened, LocalPanel: &state.props})
		}
		return nil
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
