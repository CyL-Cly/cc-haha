package agentcatalog

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	weburl "github.com/nlnwa/whatwg-url/url"
)

// Hooks are parsed here as declaration data. Running an agent-specific hook
// still requires its real lifecycle registry, never a ignored declaration.
func parseDefinitionHooks(raw json.RawMessage) json.RawMessage {
	var input protocol.JSONObject
	if input.UnmarshalJSON(raw) != nil || !input.IsObject() {
		return nil
	}
	events := strings.Fields("PreToolUse PostToolUse PostToolUseFailure PermissionRequest Notification UserPromptSubmit SessionStart SessionEnd Stop StopFailure SubagentStart SubagentStop PreCompact PostCompact Setup TeammateIdle TaskCompleted Elicitation ElicitationResult ConfigChange WorktreeCreate WorktreeRemove InstructionsLoaded PermissionDenied TaskCreated CwdChanged FileChanged")
	result := protocol.NewJSONObject()
	for _, property := range input.OwnProperties() {
		name, err := exactString(property.NameJSON)
		if err != nil {
			return nil
		}
		validEvent := false
		for _, event := range events {
			validEvent = validEvent || name == event
		}
		if !validEvent {
			return nil
		}
		var matchers []protocol.JSONObject
		if json.Unmarshal(property.Value, &matchers) != nil || matchers == nil {
			return nil
		}
		parsed := []protocol.JSONObject{}
		for _, matcher := range matchers {
			next := protocol.NewJSONObject()
			if raw := matcher.Get("matcher"); raw != nil {
				if _, err := protocol.DecodeJavaScriptJSONString(raw); err != nil {
					return nil
				}
				next.Set("matcher", raw)
			}
			var hooks []protocol.JSONObject
			if json.Unmarshal(matcher.Get("hooks"), &hooks) != nil || hooks == nil {
				return nil
			}
			commands := []protocol.JSONObject{}
			for _, hook := range hooks {
				command := parseDefinitionHook(hook)
				if !command.IsObject() {
					return nil
				}
				commands = append(commands, command)
			}
			next.Set("hooks", encodeField(commands))
			parsed = append(parsed, next)
		}
		_ = result.SetPropertyJSON(property.NameJSON, encodeField(parsed))
	}
	return encodeField(result)
}

func parseDefinitionHook(input protocol.JSONObject) protocol.JSONObject {
	output := protocol.NewJSONObject()
	kind := input.String("type")
	fields := []string{}
	switch kind {
	case "command":
		fields = []string{"type", "command", "if", "shell", "timeout", "statusMessage", "once", "async", "asyncRewake"}
	case "prompt", "agent":
		fields = []string{"type", "prompt", "if", "timeout", "model", "statusMessage", "once"}
	case "http":
		fields = []string{"type", "url", "if", "timeout", "headers", "allowedEnvVars", "statusMessage", "once"}
	default:
		return protocol.JSONObject{}
	}
	for _, name := range fields {
		raw := input.Get(name)
		if raw == nil {
			if name == "type" || name == "command" || name == "prompt" || name == "url" {
				return protocol.JSONObject{}
			}
			continue
		}
		switch name {
		case "once", "async", "asyncRewake":
			if string(raw) != "true" && string(raw) != "false" {
				return protocol.JSONObject{}
			}
		case "timeout":
			var number float64
			if json.Unmarshal(raw, &number) != nil || number <= 0 || math.IsInf(number, 0) || math.IsNaN(number) {
				return protocol.JSONObject{}
			}
		case "headers":
			var headers protocol.JSONObject
			if headers.UnmarshalJSON(raw) != nil || !headers.IsObject() {
				return protocol.JSONObject{}
			}
			parsed := protocol.NewJSONObject()
			for _, property := range headers.OwnProperties() {
				if string(property.NameJSON) == `"__proto__"` {
					continue
				}
				if _, err := protocol.DecodeJavaScriptJSONString(property.Value); err != nil {
					return protocol.JSONObject{}
				}
				_ = parsed.SetPropertyJSON(property.NameJSON, property.Value)
			}
			raw = encodeField(parsed)
		case "allowedEnvVars":
			var values []json.RawMessage
			if json.Unmarshal(raw, &values) != nil || values == nil {
				return protocol.JSONObject{}
			}
			for _, value := range values {
				if _, err := protocol.DecodeJavaScriptJSONString(value); err != nil {
					return protocol.JSONObject{}
				}
			}
		default:
			if _, err := protocol.DecodeJavaScriptJSONString(raw); err != nil {
				return protocol.JSONObject{}
			}
			if name == "shell" && input.String(name) != "bash" && input.String(name) != "powershell" {
				return protocol.JSONObject{}
			}
			if name == "url" {
				if parsed, err := weburl.Parse(input.String(name)); err != nil || parsed.Scheme() == "" {
					return protocol.JSONObject{}
				}
			}
		}
		output.Set(name, raw)
	}
	return output
}
