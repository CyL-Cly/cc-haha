package application

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

type pendingModelPicker struct {
	id       string
	config   *terminalLocalPanelState
	teammate bool
}

// InvokeModelPicker is called by the shared interactive action dispatcher.
// Typed /model dispatch and hotkeys reach this same native picker consumer.
func (s *Session) InvokeModelPicker(ctx context.Context, standalone bool, emit tui.Emit) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return cooperative.ErrClosed
	}
	previous := s.modelPicker
	if previous != nil && !standalone {
		s.modelPicker = nil
		s.mu.Unlock()
		return emit(tui.Event{Kind: tui.ModelPickerClosed})
	}
	s.mu.Unlock()
	picker, err := s.process.NativeModelPicker(ctx, standalone)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return cooperative.ErrClosed
	}
	s.modelPicker = &pendingModelPicker{id: picker.ID}
	s.mu.Unlock()
	return emit(tui.Event{Kind: tui.ModelPickerOpened, ModelPicker: &picker})
}

func (s *Session) RespondModelPicker(ctx context.Context, id string, result tui.ModelPickerResult, emit tui.Emit) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return cooperative.ErrClosed
	}
	pending := s.modelPicker
	if pending == nil || pending.id != id {
		s.mu.Unlock()
		return &tui.UnsupportedAction{Action: "stale modelPicker callback"}
	}
	s.modelPicker = nil
	s.mu.Unlock()
	var notice string
	var err error
	changed := !result.Cancelled
	if pending.teammate {
		changed, err = s.process.ApplyNativeTeammateModelPicker(ctx, id, result)
	} else if pending.config != nil {
		notice, err = s.process.ApplyNativeConfigModelPicker(ctx, id, result)
	} else {
		notice, err = s.process.ApplyNativeModelPicker(ctx, id, result)
	}
	if err != nil {
		return err
	}
	if pending.config != nil {
		state := pending.config
		state.mu.Lock()
		defer state.mu.Unlock()
		s.mu.Lock()
		active := s.terminalPanel == state
		s.mu.Unlock()
		if !active {
			return &tui.UnsupportedAction{Action: "stale Config model picker callback"}
		}
		if changed {
			if state.configDirty == nil {
				state.configDirty = map[string]bool{}
			}
			key := "model"
			if pending.teammate {
				key = "teammateDefaultModel"
			}
			state.configDirty[key] = true
		}
		if err = s.refreshTerminalPanel(ctx, state); err != nil {
			return err
		}
		return emit(tui.Event{Kind: tui.LocalPanelOpened, LocalPanel: &state.props})
	}
	if notice != "" {
		return emit(tui.Event{Kind: tui.Notice, Text: notice})
	}
	return nil
}
