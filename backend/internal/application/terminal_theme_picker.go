package application

import (
	"context"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

// A child Config picker uses ThemeProvider preview state without persisting
// focus. The original Config owner retains its mount-time rollback snapshot.
type terminalThemePickerState struct {
	parentConfig                   bool
	savedTheme                     string
	syntaxDisabled                 bool
	colorReason, syntaxEnvironment string
	colorEnvironment               string
	currentColorReason             string
}

func (s *Session) openTerminalThemePicker(ctx context.Context, state *terminalLocalPanelState) error {
	value, err := s.process.TerminalThemeState(ctx)
	if err != nil {
		return err
	}
	state.themePicker = &terminalThemePickerState{parentConfig: state.props.Kind == "config", savedTheme: value.String("theme"), syntaxDisabled: string(value.Get("syntaxHighlightingDisabled")) == "true", colorReason: value.String("colorModuleUnavailableReason"), syntaxEnvironment: value.String("syntaxEnvironment"), colorEnvironment: value.String("colorEnvironment")}
	state.themePicker.currentColorReason = value.String("colorModuleUnavailableReason")
	return s.refreshTerminalThemePicker(state)
}

func (s *Session) refreshTerminalThemePicker(state *terminalLocalPanelState) error {
	picker := state.themePicker
	if picker == nil {
		return fmt.Errorf("Theme picker is no longer active")
	}
	p := &state.props
	p.Title, p.Description = "Theme", "Choose the text style that looks best with your terminal"
	p.ThemePicker, p.Submenu, p.HideDetails = true, true, true
	p.SelectedRow = picker.savedTheme
	p.Rows, p.Fields, p.Actions = nil, nil, nil
	p.RawInputKeys = nil
	p.Presentation = raw(map[string]any{"syntaxHighlightingDisabled": picker.syntaxDisabled, "colorModuleUnavailableReason": picker.colorReason, "currentColorModuleUnavailableReason": picker.currentColorReason, "syntaxEnvironment": picker.syntaxEnvironment, "colorEnvironment": picker.colorEnvironment})
	state.rows = map[string]protocol.JSONObject{}
	for _, option := range tui.TerminalThemeOptions(false) {
		state.rows[option.ID] = protocol.NewJSONObject(protocol.JSONField{Name: "theme", Value: raw(option.ID)})
		p.Rows = append(p.Rows, tui.LocalPanelRow{ID: option.ID, Label: option.Label, Actions: []tui.LocalPanelAction{{Key: "enter", Name: "theme-select", Label: "select"}}})
	}
	return nil
}

func (s *Session) applyTerminalThemePicker(ctx context.Context, state *terminalLocalPanelState, result tui.LocalPanelResult, emit tui.Emit) (bool, error) {
	picker := state.themePicker
	if picker == nil {
		return false, nil
	}
	switch result.Action {
	case "theme-toggle-syntax":
		if picker.colorReason != "" {
			return true, nil
		}
		if err := s.process.SetTerminalThemeSyntaxHighlighting(ctx, !picker.syntaxDisabled); err != nil {
			return true, err
		}
		picker.syntaxDisabled = !picker.syntaxDisabled
		current, err := s.process.TerminalThemeState(ctx)
		if err != nil {
			return true, err
		}
		picker.currentColorReason = current.String("colorModuleUnavailableReason")
		picker.syntaxEnvironment = current.String("syntaxEnvironment")
		if picker.parentConfig {
			if state.configDirty == nil {
				state.configDirty = map[string]bool{}
			}
			state.configDirty["syntaxHighlightingDisabled"] = true
		}
		if err := s.emitTerminalConfig(ctx, emit); err != nil {
			return true, err
		}
		if err := s.refreshTerminalThemePicker(state); err != nil {
			return true, err
		}
		if emit != nil {
			return true, emit(tui.Event{Kind: tui.LocalPanelOpened, LocalPanel: &state.props})
		}
		return true, nil
	case "theme-select", "theme-cancel", "close":
		selected := result.Action == "theme-select"
		if selected {
			found := false
			for _, option := range tui.TerminalThemeOptions(false) {
				found = found || option.ID == result.Row
			}
			if !found {
				return true, fmt.Errorf("Theme option is no longer available")
			}
			// Source saves ThemeProvider's preview, then the wrapper's setTheme
			// callback saves the selected setting again.
			for i := 0; i < 2; i++ {
				if err := s.process.SetTerminalConfigSetting(ctx, "theme", raw(result.Row)); err != nil {
					return true, err
				}
			}
			if picker.parentConfig {
				if state.configDirty == nil {
					state.configDirty = map[string]bool{}
				}
				state.configDirty["theme"] = true
			}
		}
		state.themePicker = nil
		state.props.ThemePicker = false
		if err := s.emitTerminalConfig(ctx, emit); err != nil {
			return true, err
		}
		if picker.parentConfig {
			state.props.Presentation = nil
			state.props.Submenu = false
			if err := s.refreshTerminalConfigPanel(ctx, state); err != nil {
				return true, err
			}
			if emit != nil {
				return true, emit(tui.Event{Kind: tui.LocalPanelOpened, LocalPanel: &state.props})
			}
			return true, nil
		}
		s.mu.Lock()
		if s.terminalPanel == state {
			s.terminalPanel = nil
		}
		s.mu.Unlock()
		if emit != nil {
			if err := emit(tui.Event{Kind: tui.LocalPanelClosed}); err != nil {
				return true, err
			}
			if selected {
				return true, emit(tui.Event{Kind: tui.LocalCommandOutput, Text: "Theme set to " + result.Row, Background: true})
			}
			return true, emit(tui.Event{Kind: tui.LocalProcessedMessages, LocalMessages: []tui.LocalProcessedMessage{{Kind: "system", Text: "Theme picker dismissed", Level: "info"}}, Background: true})
		}
		return true, nil
	}
	return true, fmt.Errorf("Unknown theme picker action")
}
