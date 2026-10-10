package clibootstrap

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentbuiltinmetadata"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Main-thread --agent is a session selection, distinct from a spawned Agent.
// Source print applies only a custom definition's prompt and model/effort;
// its tool and permission configuration remain those of the main invocation.
type nativeMainThreadAgentState struct {
	requestedModel, requestedPrompt string
	definition                      *agentcatalog.Definition
	prompt                          json.RawMessage
	initialPrompt                   json.RawMessage
}

func (m *MainPrint) nativeMainThreadAgentType() string {
	if m.mainThreadAgent == nil || m.mainThreadAgent.definition == nil {
		return ""
	}
	return m.mainThreadAgent.definition.Name
}

func (m *MainPrint) initializeNativeMainThreadAgent() error {
	if m.agentChild || m.teamWorker != nil || m.mainThreadAgent == nil {
		return nil
	}
	settings, err := m.Startup.GetInitialSettings()
	if err != nil {
		return err
	}
	name := m.options.Arguments.Agent
	if name == "" && settings != nil {
		name = settings.String("agent")
	}
	if name == "" {
		return nil
	}
	return m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if err := m.selectNativeMainThreadAgent(scope, name); err != nil {
			return err
		}
		if m.mainThreadAgent.definition != nil {
			initial := m.mainThreadAgent.definition.Fields.Get("initialPrompt")
			if units, err := protocol.DecodeJavaScriptJSONString(initial); err == nil && len(units) > 0 {
				m.mainThreadAgent.initialPrompt = append(json.RawMessage(nil), initial...)
			}
		}
		return nil
	})
}

func (m *MainPrint) selectNativeMainThreadAgent(scope *cooperative.Scope, name string) error {
	if !scope.BelongsTo(m.Owner) || m.agents == nil || m.mainThreadAgent == nil {
		return cooperative.ErrRestricted
	}
	definitions, err := m.agents.List(scope)
	if err != nil {
		return err
	}
	for _, definition := range definitions.Active {
		if definition.Name != name {
			continue
		}
		selected := definition
		m.mainThreadAgent.definition = &selected
		if m.mainThreadAgent.requestedPrompt == "" && selected.Source != "built-in" {
			prompt, err := m.agents.PromptJSON(scope, selected, agentbuiltinmetadata.GuideContext{})
			if err != nil {
				return err
			}
			var display string
			if err = json.Unmarshal(prompt, &display); err != nil {
				return err
			}
			if display != "" {
				m.mainThreadAgent.prompt = append(json.RawMessage(nil), prompt...)
				m.options.Arguments.SystemPrompt = display
			}
		}
		if m.mainThreadAgent.requestedModel == "" && selected.Model != "" && selected.Model != "inherit" {
			settings, err := m.Startup.GetInitialSettings()
			if err != nil {
				return err
			}
			model, selector, err := m.resolveNativeModel(context.Background(), scope, settings, selected.Model)
			if err != nil {
				return err
			}
			m.options.Arguments.Model = model
			m.nativeModelSelector = selector
		}
		return nil
	}
	// Like original main, an unavailable selection uses the normal main session.
	return nil
}

func (m *MainPrint) restoreNativeMainThreadAgent(scope *cooperative.Scope) error {
	if m.agentChild || m.teamWorker != nil || m.mainThreadAgent == nil {
		return nil
	}
	if !scope.BelongsTo(m.Owner) || m.Controller == nil || m.Host == nil {
		return cooperative.ErrRestricted
	}
	if m.options.Arguments.Agent == "" && m.mainThreadAgent.definition == nil {
		metadata, _ := m.Controller.Writer().Snapshot().Object("metadata")
		if name := metadata.String("agentSetting"); name != "" {
			if err := m.selectNativeMainThreadAgent(scope, name); err != nil {
				return err
			}
		}
	}
	if m.mainThreadAgent.definition == nil {
		return nil
	}
	if len(m.mainThreadAgent.prompt) > 0 {
		if err := m.Controller.SetNativeSDKSystemPrompt(scope, m.mainThreadAgent.prompt, m.prompt.ResolveForModel); err != nil {
			return err
		}
	}
	// Restoration occurs after the Host exists and before the next actual ASK.
	if err := m.Host.SetInferenceModel(scope, m.options.Arguments.Model, nil, false); err != nil {
		return err
	}
	metadata := protocol.NewJSONObject(protocol.JSONField{Name: "agentSetting", Value: quoteMain(m.mainThreadAgent.definition.Name)})
	m.Controller.Writer().RestoreMetadata(metadata)
	return nil
}

// The permission guard admits agent configuration only on this exact native
// main graph; no bool inferred from settings presence or a foreign Host.
func (m *MainPrint) nativeMainAgentSettingsBound(scope *cooperative.Scope) bool {
	if !scope.BelongsTo(m.Owner) || m.options.Native == nil || !m.options.Native.NativeProcess || m.teamWorker != nil || m.agents == nil || m.mainThreadAgent == nil || m.Controller == nil || !m.Controller.BootstrapOwns(m.Owner) {
		return false
	}
	if !m.agentChild {
		return true
	}
	// An actual child inherits the parent's setting without re-selecting a
	// main-thread agent. Join that existing owner through the same transfer
	// graph; a detached-looking object or foreign Scope proves nothing.
	d := m.options.DetachedAgent
	if !m.detachedConstructed || d == nil || d.authParent == nil || d.transferSettings == nil || d.liveSettings == nil || d.bindTasks == nil || d.authParent == m {
		return false
	}
	bound, err := nativeAgentOwnerCall(m, scope, d.authParent, func(parentScope *cooperative.Scope) (bool, error) {
		return d.authParent.nativeMainAgentSettingsBound(parentScope), nil
	})
	return err == nil && bound
}

// The original CLI joins its string prompt here; SDK async input prepends a
// separate user record. A restored definition never schedules this message.
func (m *MainPrint) PrependNativeMainAgentInitialPrompt(ctx context.Context, prompt string) (string, error) {
	result := prompt
	err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if m.mainThreadAgent == nil || len(m.mainThreadAgent.initialPrompt) == 0 {
			return nil
		}
		var initial string
		if err := json.Unmarshal(m.mainThreadAgent.initialPrompt, &initial); err != nil {
			return err
		}
		m.mainThreadAgent.initialPrompt = nil
		if prompt == "" {
			result = initial
		} else {
			result = initial + "\n\n" + prompt
		}
		return nil
	})
	return result, err
}

func (m *MainPrint) nativeMainAgentInitialSDKLine(scope *cooperative.Scope) (string, error) {
	if !scope.BelongsTo(m.Owner) {
		return "", cooperative.ErrRestricted
	}
	if m.mainThreadAgent == nil || len(m.mainThreadAgent.initialPrompt) == 0 {
		return "", nil
	}
	content := m.mainThreadAgent.initialPrompt
	m.mainThreadAgent.initialPrompt = nil
	// main.tsx passes agentCli into print's initialize branch. A startup
	// selection from settings.agent does not prepend an async initial user.
	if m.options.Arguments.Agent == "" {
		return "", nil
	}
	message := protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoteMain("user")}, protocol.JSONField{Name: "content", Value: content})
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(message)})
	return string(rawMain(user)) + "\n", nil
}
