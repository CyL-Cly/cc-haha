package builtintools

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// PrepareAgentResume rebuilds the current original definition and tool pool.
// Resume deliberately does not reapply the spawn-time Agent deny rule.
func (s *Service) PrepareAgentResume(scope *cooperative.Scope, prompt string, metadata protocol.JSONObject) (AgentRequest, error) {
	if e := s.check(scope); e != nil {
		return AgentRequest{}, e
	}
	if metadata.String("agentType") == "fork" {
		return AgentRequest{}, capability("Agent fork resume parent cache-identical prompt")
	}
	selected := agentcatalog.Definition{Name: "general-purpose", Source: "built-in", SystemPrompt: generalPurposeSystemPrompt, Fields: protocol.NewJSONObject(field("tools", []string{"*"}))}
	if s.options.AgentDefinitions != nil {
		definitions, e := s.options.AgentDefinitions(scope)
		if e != nil {
			return AgentRequest{}, e
		}
		for _, definition := range definitions {
			if definition.Name == metadata.String("agentType") {
				selected = definition
				break
			}
		}
	}
	for _, key := range []string{"hooks", "mcpServers", "skills"} {
		if permissionFeedbackValue(selected.Fields.Get(key)) {
			return AgentRequest{}, capability("Agent configured " + key + " lifecycle")
		}
	}
	permission, e := s.options.PermissionContext(scope)
	if e != nil {
		return AgentRequest{}, e
	}
	_, tools, e := s.Catalog(scope)
	if e != nil {
		return AgentRequest{}, e
	}
	available := []string{}
	for _, tool := range tools {
		available = append(available, tool.Name)
	}
	if s.options.AgentAdditionalTools != nil {
		extras, e := s.options.AgentAdditionalTools(scope)
		if e != nil {
			return AgentRequest{}, e
		}
		for _, tool := range extras {
			available = append(available, tool.Name)
		}
	}
	resolved := agentcatalog.ResolveTools(selected, available, true, false)
	system := selected.SystemPrompt
	systemJSON := quote(system)
	if s.options.AgentPromptJSON != nil {
		systemJSON, e = s.options.AgentPromptJSON(scope, selected)
		if e != nil {
			return AgentRequest{}, e
		}
		if e = json.Unmarshal(systemJSON, &system); e != nil {
			return AgentRequest{}, e
		}
	} else if s.options.AgentPrompt != nil {
		system, e = s.options.AgentPrompt(scope, selected)
		if e != nil {
			return AgentRequest{}, e
		}
		systemJSON = quote(system)
	}

	description := "(resumed)"
	if metadata.Get("description") != nil && string(metadata.Get("description")) != "null" {
		description = metadata.String("description")
	}
	model := ""
	switch metadata.String("model") {
	case "fable", "sonnet", "opus", "haiku":
		model = metadata.String("model")
	}
	override := selected.Fields.String("permissionMode")
	if (selected.Source == "projectSettings" || selected.Source == "localSettings") && override == "bypassPermissions" {
		override = ""
	}
	childPermission := permission.Clone()
	if override != "" && permission.String("mode") != "bypassPermissions" && permission.String("mode") != "acceptEdits" && permission.String("mode") != "auto" {
		childPermission.Set("mode", quote(override))
	}
	var maxTurns *float64
	if v := selected.Fields.Get("maxTurns"); v != nil {
		var value float64
		if json.Unmarshal(v, &value) == nil {
			maxTurns = &value
		}
	}
	persistedType := protocol.TrimJavaScriptString(metadata.String("agentType"))
	if persistedType == "" {
		persistedType = selected.Name
	}
	return AgentRequest{AgentToolInvocation: true, Prompt: prompt, Description: description, ParentTools: available, AgentType: selected.Name, PersistedAgentType: persistedType, PersistedDescription: append(json.RawMessage(nil), metadata.Get("description")...), Model: model, DefinitionModel: selected.Model, Source: selected.Source, SelectedAgent: selected.Fields.Clone(), Effort: append(json.RawMessage(nil), selected.Effort...), MaxTurns: maxTurns, OmitClaudeMd: string(selected.Fields.Get("omitClaudeMd")) == "true", SystemPrompt: system + "\n\n" + agentNotes, SystemPromptSections: []json.RawMessage{append(json.RawMessage(nil), systemJSON...), quote(agentNotes)}, Tools: resolved.ResolvedTools, PermissionContext: childPermission, ParentPermissionMode: permission.String("mode"), PermissionModeOverride: override}, nil
}
