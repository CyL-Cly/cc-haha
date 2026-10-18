package clibootstrap

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentbuiltinmetadata"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// REPL resume always restores against the initial agent selection, rather than
// treating the previous resumed session's agent as an explicit CLI selection.
func (m *MainPrint) restoreTerminalResumeAgent(scope *cooperative.Scope, log protocol.JSONObject) error {
	if m.mainThreadAgent == nil || !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	state := m.mainThreadAgent
	state.definition = state.initialDefinition
	state.prompt = nil
	m.options.Arguments.SystemPrompt = state.requestedPrompt
	if state.definition != nil {
		// restoreAgentFromSession returns the captured initial definition. It
		// neither reloads the agent file nor reapplies its model over a live /model.
		if state.requestedPrompt == "" && state.definition.Source != "built-in" {
			prompt, err := m.agents.PromptJSON(scope, *state.definition, agentbuiltinmetadata.GuideContext{})
			if err != nil {
				return err
			}
			state.prompt = prompt
			var display string
			if err = json.Unmarshal(prompt, &display); err != nil {
				return err
			}
			m.options.Arguments.SystemPrompt = display
		}
	} else if name := log.String("agentSetting"); name != "" {
		// Source only applies the resumed agent's model when no current model
		// override exists. An earlier /model or selected agent keeps its override.
		model, selector := m.options.Arguments.Model, m.nativeModelSelector
		specified, err := selector.UserSpecified()
		if err != nil {
			return err
		}
		if err := m.selectNativeMainThreadAgent(scope, name); err != nil {
			return err
		}
		if units, _ := protocol.DecodeJavaScriptJSONString(specified); len(units) > 0 {
			m.options.Arguments.Model, m.nativeModelSelector = model, selector
		}
	}
	prompt := quoteMain(state.requestedPrompt)
	if len(state.prompt) > 0 {
		prompt = append(json.RawMessage(nil), state.prompt...)
	}
	if err := m.Controller.SetNativeSDKSystemPrompt(scope, prompt, m.prompt.ResolveForModel); err != nil {
		return err
	}
	return m.Host.SetInferenceModel(scope, m.options.Arguments.Model, nil, false)
}
