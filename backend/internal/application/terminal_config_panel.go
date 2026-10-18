package application

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

type terminalConfigItem struct {
	key, label, store string
	choices           []string
	fallback          json.RawMessage
}

func configBool(key, label, store string, fallback bool) terminalConfigItem {
	return terminalConfigItem{key: key, label: label, store: store, choices: []string{"true", "false"}, fallback: raw(fallback)}
}
func configItems(snapshot protocol.JSONObject) []terminalConfigItem {
	items := []terminalConfigItem{
		configBool("autoCompactEnabled", "Auto-compact", "global", true), configBool("spinnerTipsEnabled", "Show tips", "initial", true), configBool("prefersReducedMotion", "Reduce motion", "initial", false), configBool("thinkingEnabled", "Thinking mode", "runtime", true),
		{key: "instructionFiles", label: "Project instructions", store: "runtime", choices: []string{"claude-md", "claude-md-or-agents-md", "claude-md-and-agents-md", "managed-only"}, fallback: raw("claude-md-or-agents-md")},
		configBool("enableWorkflows", "Dynamic workflows", "initial", true), configBool("workflowKeywordTriggerEnabled", "Ultracode keyword trigger", "initial", true),
		{key: "workflowSizeGuideline", label: "Dynamic workflow size", store: "global", choices: []string{"unrestricted", "small", "medium", "large"}, fallback: raw("medium")},
		configBool("verbose", "Verbose output", "global", false), configBool("terminalProgressBarEnabled", "Terminal progress bar", "global", true), configBool("showTurnDuration", "Show turn duration", "global", true),
		{key: "defaultPermissionMode", label: "Default permission mode", store: "permissions", choices: []string{"default", "plan", "acceptEdits", "dontAsk"}, fallback: raw("default")},
		configBool("respectGitignore", "Respect .gitignore in file picker", "global", true), configBool("copyFullResponse", "Always copy full response (skip /copy picker)", "global", false),
		{key: "autoUpdatesChannel", label: "Auto-update channel", store: "initial", fallback: raw("latest")},
		{key: "theme", label: "Theme", store: "global", choices: terminalThemes, fallback: raw("dark")},
		{key: "preferredNotifChannel", label: "Notifications", store: "global", choices: []string{"auto", "iterm2", "terminal_bell", "iterm2_with_bell", "kitty", "ghostty", "notifications_disabled"}, fallback: raw("auto")},
		{key: "outputStyle", label: "Output style", store: "initial", fallback: raw("default")}, {key: "language", label: "Language", store: "initial", fallback: raw("")},
		{key: "editorMode", label: "Editor mode", store: "global", choices: []string{"normal", "vim"}, fallback: raw("normal")},
		configBool("prStatusFooterEnabled", "Show PR status footer", "global", true),
		{key: "model", label: "Model", store: "runtime", fallback: raw(nil)},
	}
	if string(snapshot.Get("isConnectedToIde")) == "true" {
		items = append(items, terminalConfigItem{key: "diffTool", label: "Diff tool", store: "global", choices: []string{"terminal", "auto"}, fallback: raw("auto")})
	}
	if string(snapshot.Get("isSupportedTerminal")) == "true" {
		items = append(items, configBool("autoInstallIdeExtension", "Auto-install IDE extension", "global", true))
	} else {
		items = append(items, configBool("autoConnectIde", "Auto-connect to IDE (external terminal)", "global", false))
	}
	items = append(items, configBool("claudeInChromeDefaultEnabled", "Claude in Chrome enabled by default", "global", true))
	if string(snapshot.Get("teamsEnabled")) == "true" {
		label := "Teammate mode"
		if override := snapshot.String("teammateModeOverride"); override != "" {
			label += " [overridden: " + override + "]"
		}
		items = append(items, terminalConfigItem{key: "teammateMode", label: label, store: "global", choices: []string{"auto", "tmux", "in-process"}, fallback: raw("auto")}, terminalConfigItem{key: "teammateDefaultModel", label: "Default teammate model", store: "global", fallback: raw(nil)})
	}
	if len(array(snapshot.Get("externalIncludes"))) != 0 {
		items = append(items, terminalConfigItem{key: "showExternalIncludesDialog", label: "External CLAUDE.md includes", store: "runtime", fallback: raw(false)})
	}
	if snapshot.String("apiKeyLabel") != "" {
		items = append(items, configBool("apiKey", "Use custom API key: "+snapshot.String("apiKeyLabel"), "runtime", false))
	}
	if string(snapshot.Get("checkpointingAvailable")) == "true" {
		items = append(items[:4], append([]terminalConfigItem{configBool("fileCheckpointingEnabled", "Rewind code (checkpoints)", "global", true)}, items[4:]...)...)
	}
	return items
}

var terminalThemes = []string{"dark", "light", "dark-daltonized", "light-daltonized", "dark-ansi", "light-ansi"}

func configValue(snapshot protocol.JSONObject, item terminalConfigItem) json.RawMessage {
	var value json.RawMessage
	switch item.store {
	case "runtime":
		value = snapshot.Get(item.key)
	case "permissions":
		value = obj(obj(snapshot.Get("initial")).Get("permissions")).Get("defaultMode")
	default:
		value = obj(snapshot.Get(item.store)).Get(item.key)
	}
	if item.key == "enableWorkflows" && string(obj(snapshot.Get("initial")).Get("disableWorkflows")) == "true" {
		value = []byte("false")
	}
	if value == nil {
		value = item.fallback
	}
	return value
}
func configLabel(value json.RawMessage) string {
	var text string
	if json.Unmarshal(value, &text) == nil {
		if text == "" {
			return "Default (English)"
		}
		return text
	}
	return string(value)
}
func (s *Session) refreshTerminalConfigPanel(ctx context.Context, state *terminalLocalPanelState) error {
	snapshot, err := s.process.TerminalConfigState(ctx)
	if err != nil {
		return err
	}
	if state.configSnapshot == nil {
		copy := snapshot.Clone()
		state.configSnapshot = &copy
	}
	state.props.Title = "Config"
	state.props.Rows, state.props.Fields = nil, nil
	state.props.RowDescriptions, state.props.VisibleRows = false, 0
	state.props.Presentation = nil
	state.props.ThemePicker = false
	state.rows = map[string]protocol.JSONObject{}
	state.props.Submenu = false
	state.props.HideDetails = true
	state.props.DismissAction = ""
	state.props.SelectedRow = ""
	state.props.Description = "←/→ change setting · Enter keep changes · Esc revert changes"
	state.props.Actions = []tui.LocalPanelAction{{Key: "enter", Name: "commit-config", Label: "keep changes"}}
	items := configItems(snapshot)
	if state.props.Kind == "theme" {
		items = []terminalConfigItem{{key: "theme", label: "Theme", store: "global", choices: terminalThemes, fallback: raw("dark")}}
		state.props.Title = "Theme"
		state.props.Description = "Select a theme for your terminal"
		state.props.Actions = nil
	}
	for _, item := range items {
		value := configValue(snapshot, item)
		label := configLabel(value)
		if item.key == "model" {
			label = snapshot.String("modelLabel")
		}
		if item.key == "teammateDefaultModel" {
			label = snapshot.String("teammateModelLabel")
		}
		if item.key == "autoUpdatesChannel" && len(snapshot.Get("autoUpdaterDisabledReason")) > 0 && string(snapshot.Get("autoUpdaterDisabledReason")) != "null" {
			label = "disabled"
		}
		if item.key == "editorMode" && label == "emacs" {
			label = "normal"
		}
		row := protocol.NewJSONObject(protocol.JSONField{Name: "key", Value: raw(item.key)}, protocol.JSONField{Name: "value", Value: value}, protocol.JSONField{Name: "choices", Value: raw(item.choices)})
		actions := []tui.LocalPanelAction{{Key: "right", Name: "next-config", Label: "change"}, {Key: "left", Name: "previous-config", Label: "change"}, {Key: "space", Name: "next-config", Label: "change"}}
		if item.key == "outputStyle" || item.key == "language" || item.key == "model" || item.key == "autoUpdatesChannel" || item.key == "theme" || item.key == "showExternalIncludesDialog" || item.key == "teammateDefaultModel" {
			actions = []tui.LocalPanelAction{{Key: "right", Name: "edit-config", Label: "choose"}}
		}
		state.rows[item.key] = row
		state.props.Rows = append(state.props.Rows, tui.LocalPanelRow{ID: item.key, SearchText: item.label, Label: item.label + ": " + label, Detail: fmt.Sprintf("%s\n%s", item.label, label), Actions: actions})
	}
	if state.props.Kind == "theme" {
		state.props.Rows = nil
		state.rows = map[string]protocol.JSONObject{}
		for _, theme := range terminalThemes {
			row := protocol.NewJSONObject(protocol.JSONField{Name: "theme", Value: raw(theme)})
			state.rows[theme] = row
			state.props.Rows = append(state.props.Rows, tui.LocalPanelRow{ID: theme, Label: theme, Actions: []tui.LocalPanelAction{{Key: "enter", Name: "select-theme", Label: "select"}}})
		}
	}
	return nil
}
func (s *Session) changeTerminalConfig(ctx context.Context, state *terminalLocalPanelState, row protocol.JSONObject, previous bool) error {
	var choices []string
	_ = json.Unmarshal(row.Get("choices"), &choices)
	if len(choices) == 0 {
		return fmt.Errorf("Configuration requires a picker")
	}
	current := configLabel(row.Get("value"))
	index := 0
	for i, choice := range choices {
		if choice == current {
			index = i
			break
		}
	}
	direction := 1
	if previous {
		direction = -1
	}
	next := choices[(index+len(choices)+direction)%len(choices)]
	value := raw(next)
	if next == "true" || next == "false" {
		value = []byte(next)
	}
	if err := s.process.SetTerminalConfigSetting(ctx, row.String("key"), value); err != nil {
		return err
	}
	if state.configDirty == nil {
		state.configDirty = map[string]bool{}
	}
	state.configDirty[row.String("key")] = true
	return nil
}
func (s *Session) TerminalPreferences(ctx context.Context) (tui.TerminalPreferences, error) {
	snapshot, err := s.process.TerminalUIState(ctx)
	return terminalPreferences(snapshot), err
}
func terminalPreferences(snapshot protocol.JSONObject) tui.TerminalPreferences {
	global := obj(snapshot.Get("global"))
	initial := obj(snapshot.Get("initial"))
	mode := global.String("editorMode")
	if mode == "" || mode == "emacs" {
		mode = "normal"
	}
	channel := initial.String("autoUpdatesChannel")
	if channel == "" {
		channel = "latest"
	}
	return tui.TerminalPreferences{PRStatusFooterEnabled: terminalPRSettingEnabled(global.Get("prStatusFooterEnabled")), UpdateChannel: channel, EditorMode: mode, Theme: global.String("theme"), Verbose: string(global.Get("verbose")) == "true", ShowTurnDuration: string(global.Get("showTurnDuration")) != "false", ReducedMotion: string(initial.Get("prefersReducedMotion")) == "true", ProgressBarEnabled: string(global.Get("terminalProgressBarEnabled")) != "false", ShowTips: string(initial.Get("spinnerTipsEnabled")) != "false", SpinnerTip: snapshot.String("spinnerTip")}
}
func (s *Session) emitTerminalConfig(ctx context.Context, emit tui.Emit) error {
	if emit == nil {
		return nil
	}
	preferences, err := s.TerminalPreferences(ctx)
	if err != nil {
		return err
	}
	if err = emit(tui.Event{Kind: tui.TerminalPreferencesChanged, Preferences: &preferences}); err != nil {
		return err
	}
	keyword, err := s.WorkflowKeywordEnabled(ctx)
	if err != nil {
		return err
	}
	return emit(tui.Event{Kind: tui.WorkflowKeywordChanged, Enabled: keyword})
}
