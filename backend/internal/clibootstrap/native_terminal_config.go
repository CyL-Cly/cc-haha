package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ideintegration"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
)

// TerminalConfigState is a detached view of the same parsed stores consumed by
// model context, checkpointing and workflow execution. No second config exists.
func (m *MainPrint) TerminalConfigState(ctx context.Context) (protocol.JSONObject, error) {
	var value protocol.JSONObject
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		global, err := m.Global.Get()
		if err != nil {
			return err
		}
		initial, err := m.Startup.GetInitialSettings()
		if err != nil {
			return err
		}
		user, err := m.Startup.GetSettingsForSource("userSettings")
		if err != nil {
			return err
		}
		local, err := m.Startup.GetSettingsForSource("localSettings")
		if err != nil {
			return err
		}
		if err = m.initializeNativeModelCommandState(scope); err != nil {
			return err
		}
		modelState, err := m.Controller.TypedModelStateOnOwner(scope)
		if err != nil {
			return err
		}
		_, tokens, _, enabled, err := m.terminalStatusAuth(ctx, scope)
		if err != nil {
			return err
		}
		subscriber, err := openaiauth.ClaudeSubscriber(enabled, tokens)
		if err != nil {
			return err
		}
		modelLabel, err := terminalStatusModelLabel(m.nativeModelSelector, modelState.Base, subscriber, tokens, m.nativeOpenAIEnvironment())
		if err != nil {
			return err
		}
		instructionMode, err := m.userContext.InstructionFilesMode()
		if err != nil {
			return err
		}
		thinking, _, err := m.Host.TerminalThinking(scope)
		if err != nil {
			return err
		}
		styles, err := nativeSkillWork(m, scope, func() ([]protocol.JSONObject, error) {
			rows, err := m.styles.List(ctx, m.workingDirectory())
			if err != nil {
				return nil, err
			}
			list := []protocol.JSONObject{}
			for _, style := range rows {
				list = append(list, protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quoteMain(style.Name)}, protocol.JSONField{Name: "description", Value: quoteMain(style.Description)}))
			}
			return list, nil
		})
		if err != nil {
			return err
		}
		list, err := styles.Await(scope)
		if err != nil {
			return err
		}
		value = protocol.NewJSONObject(protocol.JSONField{Name: "global", Value: rawMain(global)}, protocol.JSONField{Name: "initial", Value: rawMain(initial)}, protocol.JSONField{Name: "userSettings", Value: rawMain(user)}, protocol.JSONField{Name: "localSettings", Value: rawMain(local)}, protocol.JSONField{Name: "instructionFiles", Value: quoteMain(instructionMode)}, protocol.JSONField{Name: "thinkingEnabled", Value: rawMain(thinking.Type != "disabled")}, protocol.JSONField{Name: "styles", Value: rawMain(list)}, protocol.JSONField{Name: "checkpointingAvailable", Value: rawMain(!nativeEnvTruthy(m.options.Native.Environment["CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING"]))})
		value.Set("model", rawMain(modelState.Base))
		value.Set("modelLabel", quoteMain(modelLabel))
		value.Set("modelState", rawMain(modelState))
		value.Set("version", quoteMain(m.options.Version))
		value.Set("autoUpdaterDisabledReason", rawMain(terminalAutoUpdaterDisabledReason(m.nativeOpenAIEnvironment(), global)))
		environment := m.nativeOpenAIEnvironment()
		value.Set("isSupportedTerminal", rawMain(ideintegration.SupportedTerminal(ideintegration.Terminal(environment), environment)))
		connectedIDE := false
		if m.mcpTools != nil {
			clients, err := m.mcpTools.Status(scope)
			if err != nil {
				return err
			}
			for _, client := range clients {
				connectedIDE = connectedIDE || client.String("name") == "ide" && client.String("status") == "connected"
			}
		}
		value.Set("isConnectedToIde", rawMain(connectedIDE))
		teamsEnabled, err := m.nativeSendMessageEnabled(scope)
		if err != nil {
			return err
		}
		value.Set("teamsEnabled", rawMain(teamsEnabled))
		value.Set("teammateModeOverride", quoteMain(m.options.Arguments.TeammateMode))
		teammateModelLabel := "Default (leader's model)"
		if model := global.Get("teammateDefaultModel"); len(model) > 0 && string(model) != "null" {
			var text string
			if err := json.Unmarshal(model, &text); err != nil {
				return err
			}
			teammateModelLabel, err = nativePickerDisplay(m.nativeModelSelector, &text)
			if err != nil {
				return err
			}
		}
		value.Set("teammateModelLabel", quoteMain(teammateModelLabel))
		externals, externalApproved, err := m.terminalExternalIncludes(scope)
		if err != nil {
			return err
		}
		value.Set("externalIncludes", rawMain(externals))
		value.Set("showExternalIncludesDialog", rawMain(externalApproved))
		if key := m.nativeOpenAIEnvironment()["ANTHROPIC_API_KEY"]; key != "" && !(m.nativeOpenAIEnvironment()["USER_TYPE"] == "ant" && nativeEnvTruthy(m.nativeOpenAIEnvironment()["COO_RUNNING_ON_HOMESPACE"])) {
			value.Set("apiKeyLabel", terminalNormalizedAPIKey(key))
			approved, err := openaiauth.IsCustomAPIKeyApproved(*global, quoteMain(key))
			if err != nil {
				return err
			}
			value.Set("apiKey", rawMain(approved))
		}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return value, err
}

func terminalConfigStore(key string) string {
	switch key {
	case "autoCompactEnabled", "verbose", "fileCheckpointingEnabled", "respectGitignore", "copyFullResponse", "theme", "terminalProgressBarEnabled", "showTurnDuration", "preferredNotifChannel", "workflowSizeGuideline", "editorMode", "apiKey", "autoUpdates", "autoConnectIde", "autoInstallIdeExtension", "claudeInChromeDefaultEnabled", "diffTool", "prStatusFooterEnabled", "teammateMode", "teammateDefaultModel":
		return "global"
	case "spinnerTipsEnabled", "prefersReducedMotion", "outputStyle":
		return "localSettings"
	case "thinkingEnabled", "instructionFiles", "enableWorkflows", "workflowKeywordTriggerEnabled", "language", "defaultPermissionMode", "autoUpdatesChannel", "syntaxHighlightingDisabled":
		return "userSettings"
	}
	return ""
}
func terminalConfigChoices(key string) []string {
	switch key {
	case "autoCompactEnabled", "verbose", "fileCheckpointingEnabled", "respectGitignore", "copyFullResponse", "terminalProgressBarEnabled", "showTurnDuration", "spinnerTipsEnabled", "prefersReducedMotion", "thinkingEnabled", "enableWorkflows", "workflowKeywordTriggerEnabled", "apiKey", "autoUpdates", "syntaxHighlightingDisabled", "autoConnectIde", "autoInstallIdeExtension", "claudeInChromeDefaultEnabled", "prStatusFooterEnabled":
		return []string{"true", "false"}
	case "theme":
		return []string{"auto", "dark", "light", "dark-daltonized", "light-daltonized", "dark-ansi", "light-ansi"}
	case "instructionFiles":
		return usercontext.Modes
	case "preferredNotifChannel":
		return []string{"auto", "iterm2", "terminal_bell", "iterm2_with_bell", "kitty", "ghostty", "notifications_disabled"}
	case "workflowSizeGuideline":
		return []string{"unrestricted", "small", "medium", "large"}
	case "defaultPermissionMode":
		return []string{"default", "plan", "acceptEdits", "dontAsk"}
	case "editorMode":
		return []string{"normal", "vim"}
	case "diffTool":
		return []string{"terminal", "auto"}
	case "teammateMode":
		return []string{"auto", "tmux", "in-process"}
	}
	return nil
}
func (m *MainPrint) SetTerminalConfigSetting(ctx context.Context, key string, value json.RawMessage) error {
	store := terminalConfigStore(key)
	if store == "" {
		return errors.New("Unknown configuration setting")
	}
	if choices := terminalConfigChoices(key); len(choices) > 0 {
		text := string(value)
		if len(value) > 0 && value[0] == '"' {
			_ = json.Unmarshal(value, &text)
		}
		found := false
		for _, choice := range choices {
			found = found || text == choice
		}
		if !found {
			return errors.New("Invalid configuration choice")
		}
	}
	if !json.Valid(value) {
		return errors.New("Invalid configuration value")
	}
	if key == "outputStyle" || key == "language" {
		var text string
		if json.Unmarshal(value, &text) != nil {
			return errors.New("Configuration expects a string")
		}
	}
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if key == "outputStyle" {
			future, err := nativeSkillWork(m, scope, func() (bool, error) {
				styles, err := m.styles.List(ctx, m.workingDirectory())
				if err != nil {
					return false, err
				}
				var name string
				_ = json.Unmarshal(value, &name)
				for _, style := range styles {
					if style.Name == name {
						return true, nil
					}
				}
				return false, nil
			})
			if err != nil {
				return err
			}
			valid, err := future.Await(scope)
			if err != nil {
				return err
			}
			if !valid {
				return errors.New("Output style is no longer available")
			}
		}
		if store == "global" {
			if key == "apiKey" {
				return m.setTerminalAPIKeyApproval(string(value) == "true")
			}
			if err := m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
				next := current.Clone()
				next.Set(key, value)
				return &next, nil
			}); err != nil {
				return err
			}
			if key == "teammateMode" {
				var mode string
				_ = json.Unmarshal(value, &mode)
				m.options.Arguments.TeammateMode = ""
				m.teamLeader.mode = mode
				m.teamLeader.panes.SetMode(mode)
				m.publishTerminalTeamFooter(scope)
			}
			return nil
		}
		patch := clisettings.Patch{Object: protocol.NewJSONObject()}
		switch key {
		case "thinkingEnabled":
			if string(value) == "true" {
				patch.UndefinedPaths = [][]json.RawMessage{{quoteMain("alwaysThinkingEnabled")}}
			} else {
				patch.Object.Set("alwaysThinkingEnabled", []byte("false"))
			}
		case "enableWorkflows":
			patch.UndefinedPaths = [][]json.RawMessage{{quoteMain("disableWorkflows")}}
			if string(value) == "true" {
				patch.UndefinedPaths = append(patch.UndefinedPaths, []json.RawMessage{quoteMain("enableWorkflows")})
			} else {
				patch.Object.Set(key, []byte("false"))
			}
		case "workflowKeywordTriggerEnabled":
			if string(value) == "true" {
				patch.UndefinedPaths = [][]json.RawMessage{{quoteMain(key)}}
			} else {
				patch.Object.Set(key, value)
			}
		case "instructionFiles":
			patch.Object.Set("pluginConfigs", rawMain(map[string]any{usercontext.Plugin: map[string]any{"options": map[string]json.RawMessage{"instructionFiles": value}}}))
			patch.UndefinedPaths = [][]json.RawMessage{{quoteMain("pluginConfigs"), quoteMain(usercontext.Plugin), quoteMain("options"), quoteMain("projectInstructions")}}
		case "defaultPermissionMode":
			merged, err := m.Startup.GetInitialSettings()
			if err != nil {
				return err
			}
			permissions := protocol.NewJSONObject()
			if merged != nil {
				_ = json.Unmarshal(merged.Get("permissions"), &permissions)
			}
			permissions.Set("defaultMode", value)
			patch.Object.Set("permissions", rawMain(permissions))
		case "language":
			if string(value) == `""` {
				patch.UndefinedPaths = [][]json.RawMessage{{quoteMain("language")}}
			} else {
				patch.Object.Set(key, value)
			}
		default:
			patch.Object.Set(key, value)
		}
		result, err := m.settings.UpdateEditableSource(store, "", patch)
		if err != nil {
			return err
		}
		if result.Error != nil {
			return result.Error
		}
		if key == "thinkingEnabled" {
			if err = m.Host.SetTerminalThinking(scope, string(value) == "true"); err != nil {
				return err
			}
		}
		return m.refreshTerminalConfigConsumers(scope, key == "instructionFiles")
	})
	if err == nil {
		err = receipt.Wait()
	}
	return err
}
func (m *MainPrint) refreshTerminalConfigConsumers(scope *cooperative.Scope, refreshMemory bool) error {
	rules, err := m.nativePermissionRules()
	if err != nil {
		return err
	}
	if err = m.Host.ApplyNativeSettingsChange(scope, rules); err != nil {
		return err
	}
	if refreshMemory {
		m.userContext.RefreshMemoryFiles()
	}
	if m.styles != nil {
		m.styles.ClearAll()
	}
	if m.workflowDefinitions != nil {
		if err = m.workflowDefinitions.ClearCommandsOnOwner(scope); err != nil {
			return err
		}
	}
	m.publishNativeTerminalStatusLineChanged()
	return nil
}

// Escape restores only the keys actually changed by this panel, preserving
// other fields and concurrent writes in the user-owned shared settings files.
func (m *MainPrint) RestoreTerminalConfig(ctx context.Context, snapshot protocol.JSONObject, keys []string) error {
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		for _, key := range keys {
			if key == "showExternalIncludesDialog" {
				if err := m.restoreTerminalExternalIncludes(scope, snapshot); err != nil {
					return err
				}
				continue
			}
			if key == "model" {
				var state sessioncontroller.NativeTypedModelState
				if err := json.Unmarshal(snapshot.Get("modelState"), &state); err != nil {
					return err
				}
				env, err := m.pickerEnvironment()
				if err != nil {
					return err
				}
				initial, err := m.Startup.GetInitialSettings()
				if err != nil {
					return err
				}
				settings := protocol.NewJSONObject()
				if initial != nil {
					settings = initial.Clone()
				}
				override := rawMain(state.Base)
				selector, err := m.nativeModelSelection(ctx, scope, modelselection.Options{Environment: env, Settings: settings, Override: override})
				if err != nil {
					return err
				}
				resolved, err := nativePickerResolve(selector, state.Base)
				if err != nil {
					return err
				}
				// Config restores the original base AppState. onChangeAppState
				// persists that base again; it may differ from the initial file
				// when this session was started with --model. Effort is retained.
				if !pickerSame(m.modelPicker.rawModel, state.Base) {
					patch := clisettings.Patch{Object: protocol.NewJSONObject(protocol.JSONField{Name: "model", Value: rawMain(state.Base)})}
					if state.Base == nil {
						patch.Object.Set("model", nil)
						patch.UndefinedPaths = [][]json.RawMessage{{quoteMain("model")}}
					}
					if err = m.writePickerSetting(patch); err != nil {
						return err
					}
					if err = m.Host.AcceptInternalModelSettings(scope); err != nil {
						return err
					}
				}
				if err = m.Controller.ApplyTypedModelState(scope, state.Base, state.FastMode); err != nil {
					return err
				}
				if err = m.Controller.ApplyReplModelOverride(scope, state.ForSession); err != nil {
					return err
				}
				if err = m.Host.SetInferenceModel(scope, resolved, nil, false); err != nil {
					return err
				}
				m.modelPicker.rawModel = pickerClone(state.Base)
				m.nativeModelSelector = selector
				m.options.Arguments.Model = resolved
				continue
			}
			store := terminalConfigStore(key)
			if store == "" {
				return errors.New("Unknown configuration setting")
			}
			before := protocol.NewJSONObject()
			_ = json.Unmarshal(snapshot.Get(store), &before)
			if store == "global" {
				if key == "apiKey" {
					key = "customApiKeyResponses"
				}
				if err := m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
					next := current.Clone()
					next.Set(key, before.Get(key))
					return &next, nil
				}); err != nil {
					return err
				}
				continue
			}
			patch := clisettings.Patch{Object: protocol.NewJSONObject()}
			paths := [][]string{{key}}
			switch key {
			case "autoUpdatesChannel":
				paths = [][]string{{"autoUpdatesChannel"}, {"minimumVersion"}}
			case "thinkingEnabled":
				paths = [][]string{{"alwaysThinkingEnabled"}}
			case "instructionFiles":
				paths = [][]string{{"pluginConfigs", usercontext.Plugin, "options", "instructionFiles"}, {"pluginConfigs", usercontext.Plugin, "options", "projectInstructions"}}
			case "enableWorkflows":
				paths = [][]string{{"enableWorkflows"}, {"disableWorkflows"}}
			case "defaultPermissionMode":
				paths = [][]string{{"permissions"}}
				// An existing permissions object can still lack defaultMode.
				// A deep merge of that snapshot would retain the cancelled choice.
				if permissions, exists := before.Object("permissions"); exists && permissions.Get("defaultMode") == nil {
					patch.UndefinedPaths = append(patch.UndefinedPaths, []json.RawMessage{quoteMain("permissions"), quoteMain("defaultMode")})
				}
			}
			for _, path := range paths {
				original := before
				for _, part := range path[:len(path)-1] {
					next := protocol.NewJSONObject()
					_ = json.Unmarshal(original.Get(part), &next)
					original = next
				}
				value := original.Get(path[len(path)-1])
				encoded := []json.RawMessage{}
				for _, part := range path {
					encoded = append(encoded, quoteMain(part))
				}
				if value == nil {
					patch.UndefinedPaths = append(patch.UndefinedPaths, encoded)
				} else {
					setTerminalConfigPath(&patch.Object, path, value)
				}
			}
			result, err := m.settings.UpdateEditableSource(store, "", patch)
			if err != nil {
				return err
			}
			if result.Error != nil {
				return result.Error
			}
			if key == "thinkingEnabled" {
				if err = m.Host.SetTerminalThinking(scope, string(snapshot.Get("thinkingEnabled")) == "true"); err != nil {
					return err
				}
			}
		}
		return m.refreshTerminalConfigConsumers(scope, true)
	})
	if err == nil {
		err = receipt.Wait()
	}
	return err
}

func setTerminalConfigPath(object *protocol.JSONObject, path []string, value json.RawMessage) {
	if len(path) == 1 {
		object.Set(path[0], value)
		return
	}
	next := protocol.NewJSONObject()
	_ = json.Unmarshal(object.Get(path[0]), &next)
	setTerminalConfigPath(&next, path[1:], value)
	object.Set(path[0], rawMain(next))
}

func (m *MainPrint) TerminalUIState(ctx context.Context) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		global, err := m.Global.Get()
		if err != nil {
			return err
		}
		initial, err := m.Startup.GetInitialSettings()
		if err != nil {
			return err
		}
		result = protocol.NewJSONObject(protocol.JSONField{Name: "global", Value: rawMain(global)}, protocol.JSONField{Name: "initial", Value: rawMain(initial)})
		if global != nil && jsonNumberNative(global.Get("numStartups")) < 10 {
			result.Set("spinnerTip", quoteMain("Start with small features or bug fixes, tell Claude to propose a plan, and verify its suggested edits"))
		}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}
