package agentcatalog

import (
	"encoding/json"
	"strings"
)

type ResolvedTools struct {
	HasWildcard       bool     `json:"hasWildcard"`
	ValidTools        []string `json:"validTools"`
	InvalidTools      []string `json:"invalidTools"`
	ResolvedTools     []string `json:"resolvedTools"`
	AllowedAgentTypes []string `json:"allowedAgentTypes,omitempty"`
}

// ParseToolRule keeps Source's escaped delimiter and legacy-name semantics.
func ParseToolRule(rule string) (name, content string) {
	unescaped := func(index int) bool {
		backslashes := 0
		for i := index - 1; i >= 0 && rule[i] == '\\'; i-- {
			backslashes++
		}
		return backslashes%2 == 0
	}
	open, close := -1, -1
	for i := 0; i < len(rule); i++ {
		if rule[i] == '(' && open < 0 && unescaped(i) {
			open = i
		}
		if rule[i] == ')' && unescaped(i) {
			close = i
		}
	}
	name = rule
	if open > 0 && close > open && close == len(rule)-1 {
		name, content = rule[:open], rule[open+1:close]
		if content == "*" {
			content = ""
		}
		content = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(content, `\(`, `(`), `\)`, `)`), `\\`, `\`)
	}
	switch name {
	case "Task":
		name = "Agent"
	case "KillShell":
		name = "TaskStop"
	case "AgentOutputTool", "BashOutputTool":
		name = "TaskOutput"
	}
	return name, content
}

// ResolveTools ports the normal synchronous worker tool assembly. Async and
// main-thread variants are exposed for the actual declaration contracts.
func ResolveTools(definition Definition, available []string, async, main bool) ResolvedTools {
	return resolveTools(definition, available, async, main, false, false)
}

// ResolveInProcessTools follows the original async teammate branch. Essential
// task/message tools survive its async filter; the ordinary disallow list is
// still applied before that branch. This is a declaration, not admission.
func ResolveInProcessTools(definition Definition, available []string, internalUser bool) ResolvedTools {
	return resolveTools(definition, available, true, false, true, internalUser)
}

func resolveTools(definition Definition, available []string, async, main, inProcess, internalUser bool) ResolvedTools {
	disallowed := map[string]bool{}
	var denied []string
	_ = json.Unmarshal(definition.Fields.Get("disallowedTools"), &denied)
	for _, rule := range denied {
		name, _ := ParseToolRule(rule)
		disallowed[name] = true
	}
	mode := definition.Fields.String("permissionMode")
	allowedAsync := map[string]bool{}
	for _, name := range []string{"Read", "WebSearch", "TodoWrite", "Grep", "WebFetch", "Glob", "Bash", "PowerShell", "Edit", "Write", "NotebookEdit", "Skill", "StructuredOutput", "ToolSearch", "EnterWorktree", "ExitWorktree"} {
		allowedAsync[name] = true
	}
	filtered := []string{}
	for _, name := range available {
		if !main && !strings.HasPrefix(name, "mcp__") && !(name == "ExitPlanMode" && mode == "plan") {
			switch name {
			case "TaskOutput", "ExitPlanMode", "EnterPlanMode", "AskUserQuestion", "TaskStop", "Workflow":
				continue
			case "Agent":
				if !internalUser {
					continue
				}
			}
			if async && !allowedAsync[name] {
				allowed := false
				if inProcess {
					switch name {
					case "TaskCreate", "TaskGet", "TaskList", "TaskUpdate", "SendMessage", "Agent":
						allowed = true
					}
				}
				if !allowed {
					continue
				}
			}
		}
		if !disallowed[name] {
			filtered = append(filtered, name)
		}
	}
	var tools []string
	_ = json.Unmarshal(definition.Fields.Get("tools"), &tools)
	result := ResolvedTools{HasWildcard: tools == nil || len(tools) == 1 && tools[0] == "*", ValidTools: []string{}, InvalidTools: []string{}, ResolvedTools: []string{}}
	if result.HasWildcard {
		result.ResolvedTools = filtered
		return result
	}
	availableSet := map[string]bool{}
	for _, name := range filtered {
		availableSet[name] = true
	}
	seen := map[string]bool{}
	for _, spec := range tools {
		name, content := ParseToolRule(spec)
		if name == "Agent" {
			if content != "" {
				result.AllowedAgentTypes = []string{}
				for _, agent := range strings.Split(content, ",") {
					result.AllowedAgentTypes = append(result.AllowedAgentTypes, trimJS(agent))
				}
			}
			if !main {
				result.ValidTools = append(result.ValidTools, spec)
				continue
			}
		}
		if !availableSet[name] {
			result.InvalidTools = append(result.InvalidTools, spec)
			continue
		}
		result.ValidTools = append(result.ValidTools, spec)
		if !seen[name] {
			seen[name] = true
			result.ResolvedTools = append(result.ResolvedTools, name)
		}
	}
	return result
}
