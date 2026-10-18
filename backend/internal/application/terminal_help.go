package application

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sourcecollation"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (s *Session) refreshTerminalHelpPanel(ctx context.Context, state *terminalLocalPanelState) error {
	rows, err := s.process.TerminalHelpCommands(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	commands := tui.CloneCompletions(s.commandSnapshot)
	s.mu.Unlock()
	// Completion-only aliases remain valid entry points, but Source HelpV2's
	// commands prop holds canonical Command objects rather than alias rows.
	alias := map[string]bool{"reset": true, "new": true, "allowed-tools": true, "bashes": true, "settings": true, "plugins": true, "marketplace": true, "ios": true, "android": true, "fork": true, "continue": true, "checkpoint": true}
	for _, c := range commands {
		if c.Kind == "prompt" || alias[c.Name] {
			continue
		}
		rows = append(rows, protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: raw(c.Name)}, protocol.JSONField{Name: "description", Value: raw(c.Description)}, protocol.JSONField{Name: "builtin", Value: json.RawMessage("true")}, protocol.JSONField{Name: "hidden", Value: raw(c.Hidden)}))
	}
	builtinNames := map[string]bool{}
	for _, row := range rows {
		if string(row.Get("builtin")) == "true" {
			name, err := protocol.JavaScriptStringJSON(row.Get("name"))
			if err != nil {
				return err
			}
			builtinNames[string(name)] = true
		}
	}
	seen := map[string]bool{}
	unique := []protocol.JSONObject{}
	for _, row := range rows {
		if string(row.Get("hidden")) == "true" {
			continue
		}
		name, err := protocol.JavaScriptStringJSON(row.Get("name"))
		if err != nil {
			return err
		}
		key := string(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		row.Set("builtin", raw(builtinNames[key]))
		unique = append(unique, row)
	}
	collator, err := sourcecollation.New()
	if err != nil {
		return err
	}
	defer collator.Close()
	sort.SliceStable(unique, func(i, j int) bool {
		a, _ := protocol.DecodeJavaScriptJSONString(unique[i].Get("name"))
		b, _ := protocol.DecodeJavaScriptJSONString(unique[j].Get("name"))
		return collator.Compare(a, b) < 0
	})
	shortcuts, err := s.TerminalShortcutHelp(ctx)
	if err != nil {
		return err
	}
	version := s.process.TerminalHelpVersion()
	state.props.Title = "Claude Code v" + version
	state.props.Description = ""
	state.props.HideDetails = true
	state.props.HelpDialog = true
	state.props.Rows = nil
	state.props.Presentation = raw(tui.TerminalHelpData{Version: version, Commands: unique, Shortcuts: shortcuts})
	return nil
}

func (s *Session) applyTerminalHelpPanel(ctx context.Context, state *terminalLocalPanelState, result tui.LocalPanelResult, emit tui.Emit) (bool, error) {
	if state.props.Kind != "help" || !state.props.HelpDialog {
		return false, nil
	}
	if result.Action == "help-remount-general" {
		if err := s.refreshTerminalHelpPanel(ctx, state); err != nil {
			return true, err
		}
		if emit != nil {
			props := terminalPanelProjection(state.props)
			return true, emit(tui.Event{Kind: tui.LocalPanelRefreshed, LocalPanel: &props})
		}
		return true, nil
	}
	if result.Action != "help-close" {
		return false, nil
	}
	var completionError error
	defer func() {
		s.mu.Lock()
		if s.terminalPanel == state {
			s.terminalPanel = nil
		}
		s.mu.Unlock()
		state.helpPanel.complete(completionError)
	}()

	if emit != nil {
		if err := emit(tui.Event{Kind: tui.LocalPanelClosed, LocalPanel: &tui.LocalPanel{ID: state.props.ID, Kind: "help"}}); err != nil {
			completionError = err
			return true, err
		}
	}
	args := ""
	if state.helpPanel != nil {
		args = state.helpPanel.args
	}
	completionError = s.completeTerminalLocalJSX(ctx, "help", args, raw("Help dialog dismissed"), "system", emit)
	return true, completionError
}
