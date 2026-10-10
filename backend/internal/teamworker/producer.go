// Package teamworker translates an approved frozen worker into the same Go
// main-session harness used by CLI/desktop. It never executes a legacy CLI.
package teamworker

import (
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var EssentialTools = []string{"SendMessage", "TaskCreate", "TaskGet", "TaskList", "TaskUpdate"}

type Start struct {
	ParentSessionID, TeamName, MemberID, Name, SystemPrompt string
	Tools                                                   *[]string
	AgentDefinition                                         protocol.JSONObject
}
type Preset struct {
	Start                      Start
	Definition                 agentcatalog.Definition
	AgentID, AgentType, Source string
	ApprovedModel              string
	OmitClaudeMd               bool
	CLIBaseTools               *[]string
	MaxTurns                   json.RawMessage
}

func raw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return protocol.StringifyValidatedJavaScriptJSON(b)
}
func AddEssentialTools(tools []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, tool := range append(append([]string{}, tools...), EssentialTools...) {
		if !seen[tool] {
			out = append(out, tool)
			seen[tool] = true
		}
	}
	return out
}
func Build(start Start, model, userType string, memoryEnabled bool) (*Preset, error) {
	definition := start.AgentDefinition.Clone()
	if !definition.IsObject() {
		definition = protocol.NewJSONObject()
	}
	definition.Set("description", raw("Approved team member"))
	definition.Set("prompt", raw(start.SystemPrompt))
	definition.Set("model", raw(model))
	if start.Tools != nil {
		definition.Set("tools", raw(*start.Tools))
	} else {
		definition.Delete("tools")
	}
	parsed, e := agentcatalog.ParseJSONDefinition(start.Name, raw(definition), "flagSettings", userType, memoryEnabled)
	if e != nil {
		return nil, e
	}
	if parsed == nil {
		return nil, errors.New("Approved team worker preset is unavailable")
	}
	result := &Preset{Start: start, Definition: *parsed, AgentID: start.Name + "@" + start.TeamName, AgentType: start.Name, Source: "flagSettings", OmitClaudeMd: string(start.AgentDefinition.Get("omitClaudeMd")) == "true", MaxTurns: start.AgentDefinition.Get("maxTurns")}
	result.ApprovedModel = model
	if value := start.AgentDefinition.Get("agentType"); isString(value) {
		result.AgentType = start.AgentDefinition.String("agentType")
	}
	if value := start.AgentDefinition.Get("source"); isString(value) {
		result.Source = start.AgentDefinition.String("source")
	}
	if start.Tools != nil {
		wildcard := false
		for _, name := range *start.Tools {
			wildcard = wildcard || name == "*"
		}
		if !wildcard {
			tools := AddEssentialTools(*start.Tools)
			result.CLIBaseTools = &tools
		}
	}
	return result, nil
}
func isString(data json.RawMessage) bool {
	_, e := protocol.DecodeJavaScriptJSONString(data)
	return e == nil
}
func Environment(previous map[string]string, start Start) map[string]string {
	next := map[string]string{}
	for key, value := range previous {
		next[key] = value
	}
	for _, key := range []string{"CLAUDE_CODE_SUBAGENT_MODEL", "CLAUDE_CODE_EFFORT_LEVEL", "CLAUDE_CODE_RESUME_INTERRUPTED_TURN"} {
		delete(next, key)
	}
	next["CC_HAHA_TEAM_WORKER"] = "1"
	agentType := start.Name
	if isString(start.AgentDefinition.Get("agentType")) {
		agentType = start.AgentDefinition.String("agentType")
	}
	source := "flagSettings"
	if isString(start.AgentDefinition.Get("source")) {
		source = start.AgentDefinition.String("source")
	}
	next["CC_HAHA_TEAM_WORKER_PRESET_TYPE"] = agentType
	next["CC_HAHA_TEAM_WORKER_PRESET_SOURCE"] = source
	next["CC_HAHA_TEAM_WORKER_OMIT_CLAUDE_MD"] = "0"
	if string(start.AgentDefinition.Get("omitClaudeMd")) == "true" {
		next["CC_HAHA_TEAM_WORKER_OMIT_CLAUDE_MD"] = "1"
	}
	next["CC_HAHA_TRANSCRIPT_ENTRYPOINT"] = "claude-desktop-team-worker"
	return next
}

// Approved workers omit a persisted effort, while an explicit human-selected
// runtime effort is carried separately by the shared application arguments.
func InheritPersistedEffort(environment map[string]string) bool {
	return environment["CC_HAHA_TEAM_WORKER"] != "1"
}
