// Package teamspawn owns the original non-review teammate backend/runner
// producers. Approved desktop MAIN workers remain in teamworker.
package teamspawn

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func quoted(value string) json.RawMessage { data, _ := json.Marshal(value); return data }

// InProcessDefinition is buildInProcessTeammateAgentDefinition's declaration.
// It deliberately drops unrelated preset hooks/skills/model permissions; the
// original in-process runner composes its own full system body beforehand.
func InProcessDefinition(name, system string, preset *agentcatalog.Definition) agentcatalog.Definition {
	tools := []string{"*"}
	if preset != nil && preset.Fields.Get("tools") != nil && string(preset.Fields.Get("tools")) != "null" {
		tools = []string{}
		var declared []string
		_ = json.Unmarshal(preset.Fields.Get("tools"), &declared)
		seen := map[string]bool{}
		for _, tool := range append(declared, "SendMessage", "TeamCreate", "TeamDelete", "TaskCreate", "TaskGet", "TaskList", "TaskUpdate") {
			if !seen[tool] {
				seen[tool] = true
				tools = append(tools, tool)
			}
		}
	}
	declared, _ := json.Marshal(tools)
	fields := protocol.NewJSONObject(protocol.JSONField{Name: "agentType", Value: quoted(name)}, protocol.JSONField{Name: "whenToUse", Value: quoted("In-process teammate: " + name)}, protocol.JSONField{Name: "rawSystemPrompt", Value: quoted(system)}, protocol.JSONField{Name: "tools", Value: declared}, protocol.JSONField{Name: "source", Value: quoted("projectSettings")}, protocol.JSONField{Name: "permissionMode", Value: quoted("default")})
	result := agentcatalog.Definition{Name: name, Description: "In-process teammate: " + name, SystemPrompt: system, Source: "projectSettings", Fields: fields}
	if preset != nil {
		if model := preset.Fields.Get("model"); model != nil && string(model) != "null" && string(model) != `""` && string(model) != "false" && string(model) != "0" {
			fields.Set("model", model)
			result.Model = preset.Model
		}
		if effort := preset.Fields.Get("effort"); effort != nil {
			fields.Set("effort", effort)
			result.Effort = append(json.RawMessage(nil), effort...)
		}
	}
	return result
}
