package sdkpermissions

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflowcommands"
	"strings"
	"unicode"
)

// This is permissionSetup's explicit base-list projection. An omitted native
// list requires the complete Source registry; a partial registry cannot silently
// replace it by adding deny rules for the unported tools.
func initializePrint(scope *cooperative.Scope, owner *cooperative.Owner, settings *protocol.JSONObject, gate *workflowcommands.Gate, in Input) (*State, error) {
	if gate == nil || !in.NativeProcess && ((in.Platform != "darwin" && in.Platform != "linux") || !in.NonInteractive || (in.Model != "fixture-model" && in.Model != "deepseek-flash") || (in.Mode != "default" && in.Mode != "bypassPermissions")) || len(in.AdditionalDirectories) > 0 && !in.NativeStartup.Owns(owner) {
		return nil, cap("normal print registry/model/permission profile")
	}
	if in.NativeProcess && (in.Model == "" || (in.Platform != "darwin" && in.Platform != "linux" && in.Platform != "windows") || (in.Mode != "default" && in.Mode != "bypassPermissions" && in.Mode != "acceptEdits" && in.Mode != "plan" && in.Mode != "dontAsk")) {
		return nil, cap("native platform/model/permission mode")
	}
	if in.NativeProcess && len(in.BaseToolsCLI) == 0 && !in.NativeDefaultCatalogBindings {
		return nil, cap("complete default Source tool catalog; use an explicit supported --tools list until every enabled tool is migrated")
	}
	split := func(items []string) ([]string, error) {
		if in.NativeProcess {
			return nativeToolList(items, in.NativeMCPBindings, in.NativePlanBindings, in.NativeWebFetchBindings, in.NativeWebSearchBindings, in.NativeSkillBindings, in.NativeMCPResourceBindings, in.NativeWorktreeBindings, in.NativeBackgroundBindings, in.NativeToolSearchBindings, in.NativeSessionCollaborationBindings, in.NativeWorkflowBindings, in.NativeTeamToolBindings, in.NativeImageBindings)
		}
		out := []string{}
		for _, v := range items {
			for _, name := range strings.FieldsFunc(v, func(c rune) bool { return c == ',' || c == ' ' }) {
				if name != "Read" && name != "Write" && name != "Edit" && name != "Bash" && name != "Glob" && name != "Grep" && (!in.NativeProcess || name != "Agent") {
					return nil, cap("CLI tool registry/rule " + name)
				}
				out = append(out, name)
			}
		}
		return out, nil
	}
	bases, e := split(in.BaseToolsCLI)
	if e != nil {
		return nil, e
	}
	allowed, e := split(in.Allowed)
	if e != nil {
		return nil, e
	}
	denied, e := split(in.Disallowed)
	if e != nil {
		return nil, e
	}
	if !in.NativeProcess && len(in.BaseToolsCLI) != 1 {
		return nil, cap("explicit base tool list")
	}
	names := []string{"Agent", "TaskOutput", "Bash", "Glob", "Grep", "ExitPlanMode", "Read", "Edit", "Write", "NotebookEdit", "WebFetch", "TodoWrite", "WebSearch", "TaskStop", "AskUserQuestion", "Skill", "EnterPlanMode", "EnterWorktree", "ExitWorktree"}
	if in.NativeProcess {
		names = append(names, "TaskCreate", "TaskGet", "TaskUpdate", "TaskList")
	}
	reason, e := gate.DisabledReason(scope)
	if e != nil {
		return nil, e
	}
	if reason == "" {
		names = append(names, "Workflow")
	}
	if in.NativeImageEnabled {
		if !in.NativeImageBindings {
			return nil, cap("enabled image tools without actual native producer")
		}
		names = append(names, "ImageGen", "ImageEdit")
	}
	names = append(names, "ListMcpResourcesTool", "ReadMcpResourceTool")
	if in.NativeToolSearchBindings {
		names = append(names, "ToolSearch")
	}
	if in.NativeSessionCollaborationBindings {
		names = append(names, "ListSessions", "ReadSession", "CreateSession", "SendSessionMessage", "WaitSessions")
	}
	for _, n := range names {
		if in.NativeProcess && len(in.BaseToolsCLI) == 0 {
			break
		}
		included := false
		for _, base := range bases {
			included = included || n == base
		}
		if !included {
			denied = append(denied, n)
		}
	}
	raw := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
	ctx := protocol.NewJSONObject(protocol.JSONField{Name: "mode", Value: quote(in.Mode)}, protocol.JSONField{Name: "additionalWorkingDirectories", Value: []byte(`{"map":[]}`)}, protocol.JSONField{Name: "alwaysAllowRules", Value: raw(map[string]any{"cliArg": allowed})}, protocol.JSONField{Name: "alwaysDenyRules", Value: raw(map[string]any{"cliArg": denied})}, protocol.JSONField{Name: "alwaysAskRules", Value: []byte(`{}`)}, protocol.JSONField{Name: "isBypassPermissionsModeAvailable", Value: raw(in.Mode == "bypassPermissions" || in.AllowBypass)}, protocol.JSONField{Name: "isAutoModeAvailable", Value: []byte(`true`)})
	if in.NativeProcess {
		if settings.Get("permissions") != nil && in.PermissionRules == nil {
			return nil, cap("native permission rules with original settings-source provenance")
		}
		allowRules := map[string]json.RawMessage{"cliArg": raw(allowed)}
		denyRules := map[string]json.RawMessage{"cliArg": raw(denied)}
		askRules := map[string]json.RawMessage{}
		for source, rule := range in.PermissionRules {
			allowRules[source] = rule.nativeRulesJSON("alwaysAllowRules", false)
			denyRules[source] = rule.nativeRulesJSON("alwaysDenyRules", false)
			askRules[source] = rule.nativeRulesJSON("alwaysAskRules", false)
		}
		ctx.Set("alwaysAllowRules", raw(allowRules))
		ctx.Set("alwaysDenyRules", raw(denyRules))
		ctx.Set("alwaysAskRules", raw(askRules))
		if in.NativeStartup != nil {
			if !in.NativeStartup.Owns(owner) {
				return nil, cooperative.ErrRestricted
			}
			if e := in.NativeStartup.apply(scope, &ctx); e != nil {
				return nil, e
			}
		}
	}
	return &State{owner: owner, settings: settings, context: ctx, nativeProcess: in.NativeProcess, nativePluginBindings: in.NativePluginBindings, nativeMCPBindings: in.NativeMCPBindings, nativeWebFetchBindings: in.NativeWebFetchBindings, nativeWebSearchBindings: in.NativeWebSearchBindings, nativeWorktreeBindings: in.NativeWorktreeBindings, nativeHookBindings: in.NativeHookBindings, nativeSystemContextBindings: in.NativeSystemContextBindings, nativeTeamExecutionBindings: in.NativeTeamExecutionBindings, nativeAuthHelperBindings: in.NativeAuthHelperBindings, nativeCloudSettingsBindings: in.NativeCloudSettingsBindings, nativeWorkflowKeywordBindings: in.NativeWorkflowKeywordBindings, nativeInternalReadBindings: in.NativeInternalReadBindings, nativeSandboxBindings: in.NativeSandboxBindings, nativeModelCommandBindings: in.NativeModelCommandBindings, nativeMainAgentBindings: in.NativeMainAgentBindings, nativeStartup: in.NativeStartup}, nil
}

// Source parseToolListFromCLI uses comma/ASCII-space separators outside
// parentheses, preserving spaces and commas in rule content.
func nativeToolList(items []string, mcpBinding ...bool) ([]string, error) {
	boundMCP := len(mcpBinding) > 0 && mcpBinding[0]
	boundPlan := len(mcpBinding) > 1 && mcpBinding[1]
	boundFetch := len(mcpBinding) > 2 && mcpBinding[2]
	boundSearch := len(mcpBinding) > 3 && mcpBinding[3]
	boundSkill := len(mcpBinding) > 4 && mcpBinding[4]
	boundResources := len(mcpBinding) > 5 && mcpBinding[5]
	boundWorktree := len(mcpBinding) > 6 && mcpBinding[6]
	boundBackground := len(mcpBinding) > 7 && mcpBinding[7]
	boundSearchTool := len(mcpBinding) > 8 && mcpBinding[8]
	boundCollaboration := len(mcpBinding) > 9 && mcpBinding[9]
	boundWorkflow := len(mcpBinding) > 10 && mcpBinding[10]
	boundTeamTools := len(mcpBinding) > 11 && mcpBinding[11]
	boundImages := len(mcpBinding) > 12 && mcpBinding[12]
	out := []string{}
	for _, item := range items {
		var current strings.Builder
		parens := false
		push := func() error {
			rule := strings.TrimFunc(current.String(), func(r rune) bool { return unicode.IsSpace(r) && r != 0x85 || r == 0xfeff })
			current.Reset()
			if rule == "" {
				return nil
			}
			name := rule
			if i := strings.IndexByte(name, '('); i >= 0 {
				name = name[:i]
			}
			if name == "Task" {
				rule = "Agent" + rule[len(name):]
				name = "Agent"
			}
			switch name {
			case "ImageGen", "ImageEdit":
				if !boundImages {
					return cap("CLI Image tool without actual same-owner file/auth/fetch producer")
				}
			case "TeamCreate", "TeamDelete", "TeamPlan":
				if !boundTeamTools {
					return cap("CLI Team tool without actual manager/plan/session consumers")
				}
			case "SendMessage":
				if !boundBackground {
					return cap("CLI SendMessage without actual continuation/mailbox producer")
				}
			case "Workflow":
				if !boundWorkflow {
					return cap("CLI Workflow without actual shared orchestration/task/Agent/journal producer")
				}
			case "ListSessions", "ReadSession", "CreateSession", "SendSessionMessage", "WaitSessions":
				if !boundCollaboration {
					return cap("CLI session collaboration without actual authenticated desktop bridge")
				}
			case "ToolSearch":
				if !boundSearchTool {
					return cap("CLI ToolSearch without actual owning catalogue/search/request producer")
				}
			case "TaskOutput", "TaskStop", "AgentOutputTool", "BashOutputTool", "KillShell":
				if !boundBackground {
					return cap("CLI background tool without actual task producer")
				}
				if name == "KillShell" {
					rule = "TaskStop" + rule[len(name):]
				} else if name == "AgentOutputTool" || name == "BashOutputTool" {
					rule = "TaskOutput" + rule[len(name):]
				}
			case "EnterWorktree", "ExitWorktree":
				if !boundWorktree {
					return cap("CLI Worktree tool without actual session/git/cwd/persistence bindings")
				}
			case "ListMcpResourcesTool", "ReadMcpResourceTool":
				if !boundResources {
					return cap("CLI MCP resource tools without actual catalogue/read/persistence bindings")
				}
			case "Skill":
				if !boundSkill {
					return cap("CLI Skill tool without actual command resolver and executor")
				}
			case "WebFetch":
				if !boundFetch {
					return cap("CLI WebFetch tool without actual model/network bindings")
				}
			case "WebSearch":
				if !boundSearch {
					return cap("CLI WebSearch tool without actual model/network bindings")
				}
			case "EnterPlanMode", "ExitPlanMode":
				if !boundPlan {
					return cap("CLI PlanMode tool without actual shared host")
				}
			case "Read", "Write", "Edit", "NotebookEdit", "Bash", "Glob", "Grep", "Agent", "TodoWrite", "AskUserQuestion", "TaskCreate", "TaskGet", "TaskUpdate", "TaskList":
			default:
				if !boundMCP || !strings.HasPrefix(name, "mcp__") {
					return cap("CLI tool registry/rule " + name)
				}
			}
			if strings.HasSuffix(rule, "(*)") {
				rule = strings.TrimSuffix(rule, "(*)")
			}
			if strings.HasSuffix(rule, "()") {
				rule = strings.TrimSuffix(rule, "()")
			}
			out = append(out, rule)
			return nil
		}
		for _, char := range item {
			switch char {
			case '(':
				parens = true
				current.WriteRune(char)
			case ')':
				parens = false
				current.WriteRune(char)
			case ',', ' ':
				if parens {
					current.WriteRune(char)
				} else if e := push(); e != nil {
					return nil, e
				}
			default:
				current.WriteRune(char)
			}
		}
		if e := push(); e != nil {
			return nil, e
		}
	}
	return out, nil
}
