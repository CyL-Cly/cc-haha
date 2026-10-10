package queryhooks

import (
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type JSONOutputContext struct {
	Command, HookName, ToolUseID, Event, ExpectedEvent string
	Stdout, Stderr                                     *string
	ExitCode                                           *int
	DurationMillis                                     *float64
}
type JSONOutputResult struct {
	Fields     protocol.JSONObject
	Attachment protocol.JSONObject
}

func hookTruthy(raw json.RawMessage) bool {
	return raw != nil && string(raw) != "null" && string(raw) != "false" && string(raw) != "0" && string(raw) != "-0" && string(raw) != `""`
}

// InterpretJSONOutput is the native processHookJSONOutput business stage.
// Its input is parsed by the shared actual HookJSONOutput schema; the message
// factory remains the owning Harness's allocation boundary.
func InterpretJSONOutput(value protocol.JSONObject, c JSONOutputContext) (JSONOutputResult, error) {
	result := protocol.NewJSONObject()
	block := func(reason json.RawMessage, fallback string) {
		if !hookTruthy(reason) {
			reason = commandJSON(fallback)
		}
		result.Set("blockingError", commandJSON(protocol.NewJSONObject(protocol.JSONField{Name: "blockingError", Value: reason}, protocol.JSONField{Name: "command", Value: commandJSON(c.Command)})))
	}
	if string(value.Get("continue")) == "false" {
		result.Set("preventContinuation", []byte("true"))
		if hookTruthy(value.Get("stopReason")) {
			result.Set("stopReason", value.Get("stopReason"))
		}
	}
	if decision := value.String("decision"); decision != "" {
		switch decision {
		case "approve":
			result.Set("permissionBehavior", commandJSON("allow"))
		case "block":
			result.Set("permissionBehavior", commandJSON("deny"))
			block(value.Get("reason"), "Blocked by hook")
		default:
			return JSONOutputResult{}, fmt.Errorf("Unknown hook decision type: %s. Valid types are: approve, block", decision)
		}
	}
	if hookTruthy(value.Get("systemMessage")) {
		result.Set("systemMessage", value.Get("systemMessage"))
	}
	specific, hasSpecific := value.Object("hookSpecificOutput")
	if hasSpecific && specific.String("hookEventName") == "PreToolUse" && specific.String("permissionDecision") != "" {
		switch specific.String("permissionDecision") {
		case "allow", "ask":
			result.Set("permissionBehavior", specific.Get("permissionDecision"))
		case "deny":
			result.Set("permissionBehavior", commandJSON("deny"))
			block(value.Get("reason"), "Blocked by hook")
		default:
			return JSONOutputResult{}, fmt.Errorf("Unknown hook permissionDecision type: %s. Valid types are: allow, deny, ask", specific.String("permissionDecision"))
		}
	}
	if result.Get("permissionBehavior") != nil && value.Get("reason") != nil {
		result.Set("hookPermissionDecisionReason", value.Get("reason"))
	}
	if hasSpecific {
		event := specific.String("hookEventName")
		if c.ExpectedEvent != "" && event != c.ExpectedEvent {
			return JSONOutputResult{}, fmt.Errorf("Hook returned incorrect event name: expected '%s' but got '%s'. Full stdout: %s", c.ExpectedEvent, event, prettyHookJSON(commandJSON(value)))
		}
		switch event {
		case "PreToolUse":
			if specific.String("permissionDecision") != "" {
				switch specific.String("permissionDecision") {
				case "allow", "ask":
					result.Set("permissionBehavior", specific.Get("permissionDecision"))
				case "deny":
					result.Set("permissionBehavior", commandJSON("deny"))
					reason := specific.Get("permissionDecisionReason")
					if !hookTruthy(reason) {
						reason = value.Get("reason")
					}
					block(reason, "Blocked by hook")
				}
			}
			result.Set("hookPermissionDecisionReason", specific.Get("permissionDecisionReason"))
			if hookTruthy(specific.Get("updatedInput")) {
				result.Set("updatedInput", specific.Get("updatedInput"))
			}
			result.Set("additionalContext", specific.Get("additionalContext"))
		case "UserPromptSubmit", "Setup", "SubagentStart", "PostToolUseFailure":
			result.Set("additionalContext", specific.Get("additionalContext"))
		case "SessionStart":
			result.Set("additionalContext", specific.Get("additionalContext"))
			result.Set("initialUserMessage", specific.Get("initialUserMessage"))
			if hookTruthy(specific.Get("watchPaths")) {
				result.Set("watchPaths", specific.Get("watchPaths"))
			}
		case "PostToolUse":
			result.Set("additionalContext", specific.Get("additionalContext"))
			if hookTruthy(specific.Get("updatedMCPToolOutput")) {
				result.Set("updatedMCPToolOutput", specific.Get("updatedMCPToolOutput"))
			}
		case "PermissionDenied":
			result.Set("retry", specific.Get("retry"))
		case "PermissionRequest":
			if raw := specific.Get("decision"); hookTruthy(raw) {
				result.Set("permissionRequestResult", raw)
				d, _ := specific.Object("decision")
				behavior := "deny"
				if d.String("behavior") == "allow" {
					behavior = "allow"
				}
				result.Set("permissionBehavior", commandJSON(behavior))
				if behavior == "allow" && hookTruthy(d.Get("updatedInput")) {
					result.Set("updatedInput", d.Get("updatedInput"))
				}
			}
		case "Elicitation", "ElicitationResult":
			if action := specific.Get("action"); hookTruthy(action) {
				key := "elicitationResponse"
				fallback := "Elicitation denied by hook"
				if event == "ElicitationResult" {
					key = "elicitationResultResponse"
					fallback = "Elicitation result blocked by hook"
				}
				result.Set(key, commandJSON(protocol.NewJSONObject(protocol.JSONField{Name: "action", Value: action}, protocol.JSONField{Name: "content", Value: specific.Get("content")})))
				if specific.String("action") == "decline" {
					block(value.Get("reason"), fallback)
				}
			}
		}
	}
	kind := "hook_success"
	if result.Get("blockingError") != nil {
		kind = "hook_blocking_error"
	}
	attachment := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: commandJSON(kind)}, protocol.JSONField{Name: "hookName", Value: commandJSON(c.HookName)}, protocol.JSONField{Name: "toolUseID", Value: commandJSON(c.ToolUseID)}, protocol.JSONField{Name: "hookEvent", Value: commandJSON(c.Event)})
	if kind == "hook_blocking_error" {
		attachment.Set("blockingError", result.Get("blockingError"))
	} else {
		attachment.Set("content", commandJSON(""))
		if c.Stdout != nil {
			attachment.Set("stdout", commandJSON(*c.Stdout))
		}
		if c.Stderr != nil {
			attachment.Set("stderr", commandJSON(*c.Stderr))
		}
		if c.ExitCode != nil {
			attachment.Set("exitCode", commandJSON(*c.ExitCode))
		}
		attachment.Set("command", commandJSON(c.Command))
		if c.DurationMillis != nil {
			attachment.Set("durationMs", commandJSON(*c.DurationMillis))
		}
	}
	return JSONOutputResult{Fields: result, Attachment: attachment}, nil
}
