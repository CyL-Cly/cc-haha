// Package agenthook implements the original agent Hook result and query inputs.
// Actual model/tool execution is provided by the shared owning Harness.
package agenthook

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/prompthook"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"unicode/utf16"
)

func jsonValue(value any) json.RawMessage { data, _ := json.Marshal(value); return data }
func payload(kind, name, event, id string, fields ...protocol.JSONField) *protocol.JSONObject {
	value := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: jsonValue(kind)}, protocol.JSONField{Name: "hookName", Value: jsonValue(name)}, protocol.JSONField{Name: "toolUseID", Value: jsonValue(id)}, protocol.JSONField{Name: "hookEvent", Value: jsonValue(event)})
	for _, field := range fields {
		value.Set(field.Name, field.Value)
	}
	return &value
}

func ParseStructuredResult(raw json.RawMessage) (*protocol.JSONObject, bool) {
	var value protocol.JSONObject
	if value.UnmarshalJSON(raw) != nil || !value.IsObject() {
		return nil, false
	}
	if string(value.Get("ok")) != "true" && string(value.Get("ok")) != "false" {
		return nil, false
	}
	if reason := value.Get("reason"); reason != nil {
		if _, err := protocol.DecodeJavaScriptJSONString(reason); err != nil {
			return nil, false
		}
	}
	result := protocol.NewJSONObject(protocol.JSONField{Name: "ok", Value: value.Get("ok")}, protocol.JSONField{Name: "reason", Value: value.Get("reason")})
	return &result, true
}

func Interpret(prompt, name, event, id string, result *protocol.JSONObject, failure error, aborted, hitMax bool) prompthook.Result {
	if failure != nil {
		if aborted {
			return prompthook.Result{Outcome: "cancelled"}
		}
		return prompthook.Result{Outcome: "non_blocking_error", Payload: payload("hook_non_blocking_error", name, event, id, protocol.JSONField{Name: "stderr", Value: jsonValue("Error executing agent hook: " + failure.Error())}, protocol.JSONField{Name: "stdout", Value: jsonValue("")}, protocol.JSONField{Name: "exitCode", Value: jsonValue(1)})}
	}
	if result == nil || hitMax {
		return prompthook.Result{Outcome: "cancelled"}
	}
	if string(result.Get("ok")) == "false" {
		reason := result.Get("reason")
		units := utf16.Encode([]rune("undefined"))
		if reason != nil {
			units, _ = protocol.DecodeJavaScriptJSONString(reason)
		}
		text := protocol.QuoteJavaScriptUTF16(append(utf16.Encode([]rune("Agent hook condition was not met: ")), units...))
		return prompthook.Result{Outcome: "blocking", BlockingError: "Agent hook condition was not met: " + string(utf16.Decode(units)), BlockingErrorRaw: text, Command: prompt}
	}
	return prompthook.Result{Outcome: "success", Payload: payload("hook_success", name, event, id, protocol.JSONField{Name: "content", Value: jsonValue("")})}
}

func SystemPrompt(transcript string) string {
	return "You are verifying a stop condition in Claude Code. Your task is to verify that the agent completed the given plan. The conversation transcript is available at: " + transcript + "\n" + `You can read this file to analyze the conversation history if needed.

Use the available tools to inspect the codebase and verify the condition.
Use as few steps as possible - be efficient and direct.

When done, return your result using the StructuredOutput tool with:
- ok: true if the condition is met
- ok: false with reason if the condition is not met`
}

const StructuredPrompt = "Use this tool to return your verification result. You MUST call this tool exactly once at the end of your response."

var StructuredSchema = json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean","description":"Whether the condition was met"},"reason":{"type":"string","description":"Reason, if the condition was not met"}},"required":["ok"],"additionalProperties":false}`)
