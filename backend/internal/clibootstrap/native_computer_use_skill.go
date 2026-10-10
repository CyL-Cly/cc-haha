package clibootstrap

import (
	"context"
	_ "embed"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

//go:embed assets/computer-use-skill.md
var computerUseSkillPrompt string

type nativeComputerUseSkillGate struct {
	present, value bool
	at             float64
}

func (m *MainPrint) invalidateComputerUseSkillGate() {
	m.computerUseSkillGate = nativeComputerUseSkillGate{}
}
func (m *MainPrint) computerUseSkillEnabled() bool {
	now := m.options.Native.NowMillis()
	cached := m.computerUseSkillGate
	if cached.present && now-cached.at < 3000 {
		return cached.value
	}
	value := m.computeComputerUseSkillEnabled()
	m.computerUseSkillGate = nativeComputerUseSkillGate{present: true, value: value, at: now}
	return value
}
func (m *MainPrint) computeComputerUseSkillEnabled() bool {
	if m.options.Platform != "darwin" || !computerUseEnvironmentEnabled(m.options.Native.Environment) {
		return false
	}
	data, e := m.options.Native.readFile(m.computerUseConfigPath())
	if e != nil {
		return false
	}
	var config protocol.JSONObject
	if config.UnmarshalJSON(data) != nil {
		return false
	}
	// The original synchronous skill gate uses config?.enabled ?? false. It
	// deliberately does not use strict setup validation or an app allowlist.
	value := config.Get("enabled")
	if len(value) == 0 || string(value) == "null" {
		return false
	}
	var decoded any
	if json.Unmarshal(value, &decoded) != nil {
		return false
	}
	switch v := decoded.(type) {
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		return v != ""
	default:
		return true
	}
}
func (m *MainPrint) nativeComputerUseSkill() (skillcommands.Definition, bool, error) {
	if !m.computerUseSkillEnabled() {
		return skillcommands.Definition{}, false, nil
	}
	metadata := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("prompt")}, protocol.JSONField{Name: "name", Value: quoteMain("computer-use")}, protocol.JSONField{Name: "description", Value: quoteMain("Operate apps on the user's Mac — click, type, scroll and read app state through the accessibility engine. For native desktop apps and cross-app workflows. Prefer a purpose-built MCP server, the Chrome extension, or a CLI when one covers the task.")}, protocol.JSONField{Name: "hasUserSpecifiedDescription", Value: json.RawMessage("true")}, protocol.JSONField{Name: "allowedTools", Value: json.RawMessage(`["mcp__computer-use__js","mcp__computer-use__js_reset"]`)}, protocol.JSONField{Name: "whenToUse", Value: quoteMain("When the user wants something done inside a Mac application — playing music, filling a form, navigating an app UI, reading what is on screen. Invoke this BEFORE the first mcp__computer-use__* call; it carries the workflow those tools assume.")}, protocol.JSONField{Name: "disableModelInvocation", Value: json.RawMessage("false")}, protocol.JSONField{Name: "userInvocable", Value: json.RawMessage("true")}, protocol.JSONField{Name: "contentLength", Value: json.RawMessage("0")}, protocol.JSONField{Name: "source", Value: quoteMain("bundled")}, protocol.JSONField{Name: "loadedFrom", Value: quoteMain("bundled")}, protocol.JSONField{Name: "isHidden", Value: json.RawMessage("false")}, protocol.JSONField{Name: "progressMessage", Value: quoteMain("running")})
	definition, e := skillcommands.NewBundled(metadata, computerUseSkillPrompt, func(ctx context.Context, args *string) (string, error) {
		if e := ctx.Err(); e != nil {
			return "", e
		}
		prompt := computerUseSkillPrompt
		if args != nil && *args != "" {
			prompt += "\n## Task\n\n" + *args + "\n"
		}
		return prompt, nil
	})
	return definition, e == nil, e
}
