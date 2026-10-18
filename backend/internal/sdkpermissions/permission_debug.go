package sdkpermissions

import (
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type UnreachablePermissionRule struct {
	Source, Behavior        string
	Rule                    protocol.JSONObject
	Reason, Fix, ShadowType string
}

type permissionDebugRule struct {
	source string
	value  protocol.JSONObject
}

func permissionDebugRules(context protocol.JSONObject, key string) []permissionDebugRule {
	set, _ := context.Object(key)
	var result []permissionDebugRule
	// Original getAllow/Ask/DenyRules deliberately use the declared source
	// order, rather than object insertion order or generic map iteration.
	for _, source := range []string{"userSettings", "projectSettings", "localSettings", "flagSettings", "policySettings", "cliArg", "command", "session"} {
		var values []json.RawMessage
		if json.Unmarshal(set.Get(source), &values) != nil {
			continue
		}
		for _, value := range values {
			rule, err := ParsePermissionRuleValue(value)
			if err == nil {
				result = append(result, permissionDebugRule{source: source, value: rule})
			}
		}
	}
	return result
}

func PermissionRuleSourceDisplay(source string) string {
	switch source {
	case "userSettings":
		return "user settings"
	case "projectSettings":
		return "shared project settings"
	case "localSettings":
		return "project local settings"
	case "flagSettings":
		return "command line arguments"
	case "policySettings":
		return "enterprise managed settings"
	case "cliArg":
		return "CLI argument"
	case "command":
		return "command configuration"
	case "session":
		return "current session"
	}
	return ""
}

func PermissionRuleDisplay(value protocol.JSONObject) string {
	name, content := value.String("toolName"), value.String("ruleContent")
	if content == "" {
		return name
	}
	content = strings.ReplaceAll(content, "\\", "\\\\")
	content = strings.ReplaceAll(content, "(", "\\(")
	content = strings.ReplaceAll(content, ")", "\\)")
	return name + "(" + content + ")"
}

func PermissionModeDisplay(mode string) string {
	switch mode {
	case "plan":
		return "Plan Mode"
	case "acceptEdits":
		return "Accept edits"
	case "bypassPermissions":
		return "Bypass Permissions"
	case "dontAsk":
		return "Don't Ask"
	case "auto":
		return "Auto mode"
	default:
		return "Default"
	}
}

// DetectUnreachablePermissionRules is a read-only display consumer. It does not
// approve, deny, delete or rewrite any rule in the real permission context.
func DetectUnreachablePermissionRules(context protocol.JSONObject, sandboxAutoAllow bool) []UnreachablePermissionRule {
	allow := permissionDebugRules(context, "alwaysAllowRules")
	deny := permissionDebugRules(context, "alwaysDenyRules")
	ask := permissionDebugRules(context, "alwaysAskRules")
	result := []UnreachablePermissionRule{}
	for _, candidate := range allow {
		if candidate.value.Get("ruleContent") == nil {
			continue
		}
		for _, group := range []struct {
			kind  string
			rules []permissionDebugRule
		}{{"deny", deny}, {"ask", ask}} {
			var shadow *permissionDebugRule
			for i := range group.rules {
				other := &group.rules[i]
				if other.value.String("toolName") == candidate.value.String("toolName") && other.value.Get("ruleContent") == nil {
					shadow = other
					break
				}
			}
			if shadow == nil {
				continue
			}
			if group.kind == "ask" && candidate.value.String("toolName") == "Bash" && sandboxAutoAllow && shadow.source != "projectSettings" && shadow.source != "policySettings" && shadow.source != "command" {
				continue
			}
			name, source := shadow.value.String("toolName"), PermissionRuleSourceDisplay(shadow.source)
			prefix := "Shadowed"
			if group.kind == "deny" {
				prefix = "Blocked"
			}
			result = append(result, UnreachablePermissionRule{
				Source: candidate.source, Behavior: "allow", Rule: candidate.value.Clone(), ShadowType: group.kind,
				Reason: prefix + " by \"" + name + "\" " + group.kind + " rule (from " + source + ")",
				Fix:    "Remove the \"" + name + "\" " + group.kind + " rule from " + source + ", or remove the specific allow rule from " + PermissionRuleSourceDisplay(candidate.source),
			})
			break
		}
	}
	return result
}
