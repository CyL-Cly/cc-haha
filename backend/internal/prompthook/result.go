// Package prompthook ports Source execPromptHook's result interpretation.
// Model execution and native attachment creation are supplied by the Harness.
package prompthook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/goalstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const SystemPrompt = `You are evaluating a hook in Claude Code.

Your response must be a JSON object matching one of the following schemas:
1. If the condition is met, return: {"ok": true}
2. If the condition is not met, return: {"ok": false, "reason": "Reason for why it is not met"}`

type Result struct {
	Outcome             string
	Payload             *protocol.JSONObject
	BlockingError       string
	BlockingErrorRaw    json.RawMessage
	Command             string
	PreventContinuation bool
	StopReason          json.RawMessage
}

func raw(value any) json.RawMessage { data, _ := json.Marshal(value); return data }
func trim(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		return r >= 9 && r <= 13 || r == 32 || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
	})
}
func attachment(kind, name, event, id string, fields ...protocol.JSONField) *protocol.JSONObject {
	value := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw(kind)}, protocol.JSONField{Name: "hookName", Value: raw(name)}, protocol.JSONField{Name: "toolUseID", Value: raw(id)}, protocol.JSONField{Name: "hookEvent", Value: raw(event)})
	for _, field := range fields {
		value.Set(field.Name, field.Value)
	}
	return &value
}
func goalFailure(prompt, reason string) *Result {
	if !goalstate.IsGoalPrompt(prompt) {
		return nil
	}
	message := "Goal evaluator failed: " + reason + ". Treat the goal as incomplete and continue working toward it."
	return &Result{Outcome: "blocking", BlockingError: message, Command: prompt, PreventContinuation: true, StopReason: raw(message)}
}
func jsonKind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "boolean"
	case json.Number:
		return "number"
	case []any:
		return "array"
	default:
		return "object"
	}
}
func invalidType(expected, received string, path []string) protocol.JSONObject {
	return protocol.NewJSONObject(protocol.JSONField{Name: "expected", Value: raw(expected)}, protocol.JSONField{Name: "code", Value: raw("invalid_type")}, protocol.JSONField{Name: "path", Value: raw(path)}, protocol.JSONField{Name: "message", Value: raw("Invalid input: expected " + expected + ", received " + received)})
}
func schemaError(value any) string {
	issues := []protocol.JSONObject{}
	object, ok := value.(map[string]any)
	if !ok {
		issues = append(issues, invalidType("object", jsonKind(value), []string{}))
	} else {
		if flag, exists := object["ok"]; !exists {
			issues = append(issues, invalidType("boolean", "undefined", []string{"ok"}))
		} else if _, valid := flag.(bool); !valid {
			issues = append(issues, invalidType("boolean", jsonKind(flag), []string{"ok"}))
		}
		if reason, exists := object["reason"]; exists {
			if _, valid := reason.(string); !valid {
				issues = append(issues, invalidType("string", jsonKind(reason), []string{"reason"}))
			}
		}
	}
	if len(issues) == 0 {
		return ""
	}
	data, _ := json.Marshal(issues)
	data, _ = protocol.ParseAndStringifyJavaScriptJSON(data)
	var result bytes.Buffer
	_ = json.Indent(&result, data, "", "  ")
	return result.String()
}

// Interpret preserves Goal's fail-closed evaluator behavior and ordinary
// prompt hooks' non-blocking validation errors. Timeout and parent abort are
// distinct: a user cancellation must never become a new goal continuation.
func Interpret(prompt, name, event, id, response string, failure error, parentAborted, timedOut bool) Result {
	if failure != nil {
		if parentAborted || timedOut {
			if !parentAborted {
				if result := goalFailure(prompt, "evaluation timed out"); result != nil {
					return *result
				}
			}
			return Result{Outcome: "cancelled"}
		}
		message := failure.Error()
		reason := message
		if reason == "" {
			reason = "evaluation failed"
		}
		if result := goalFailure(prompt, reason); result != nil {
			return *result
		}
		return Result{Outcome: "non_blocking_error", Payload: attachment("hook_non_blocking_error", name, event, id, protocol.JSONField{Name: "stderr", Value: raw("Error executing prompt hook: " + message)}, protocol.JSONField{Name: "stdout", Value: raw("")}, protocol.JSONField{Name: "exitCode", Value: raw(1)})}
	}
	response = trim(response)
	decoder := json.NewDecoder(strings.NewReader(response))
	decoder.UseNumber()
	var value any
	err := decoder.Decode(&value)
	valid := err == nil
	var trailing any
	if valid && decoder.Decode(&trailing) != io.EOF {
		valid = false
	}
	falsy := value == nil
	switch v := value.(type) {
	case bool:
		falsy = !v
	case string:
		falsy = v == ""
	case json.Number:
		n, _ := strconv.ParseFloat(string(v), 64)
		falsy = n == 0
	}
	if !valid || falsy {
		if result := goalFailure(prompt, "response was not valid JSON"); result != nil {
			return *result
		}
		return Result{Outcome: "non_blocking_error", Payload: attachment("hook_non_blocking_error", name, event, id, protocol.JSONField{Name: "stderr", Value: raw("JSON validation failed")}, protocol.JSONField{Name: "stdout", Value: raw(response)}, protocol.JSONField{Name: "exitCode", Value: raw(1)})}
	}
	if message := schemaError(value); message != "" {
		if result := goalFailure(prompt, "response did not match the expected schema ("+message+")"); result != nil {
			return *result
		}
		return Result{Outcome: "non_blocking_error", Payload: attachment("hook_non_blocking_error", name, event, id, protocol.JSONField{Name: "stderr", Value: raw("Schema validation failed: " + message)}, protocol.JSONField{Name: "stdout", Value: raw(response)}, protocol.JSONField{Name: "exitCode", Value: raw(1)})}
	}
	object := value.(map[string]any)
	if !object["ok"].(bool) {
		reason := "undefined"
		var reasonRaw json.RawMessage
		if s, ok := object["reason"].(string); ok {
			reason = s
			var original protocol.JSONObject
			_ = original.UnmarshalJSON([]byte(response))
			reasonRaw = original.Get("reason")
		}
		blocking := raw(fmt.Sprintf("Prompt hook condition was not met: %s", reason))
		if len(reasonRaw) > 0 {
			units, _ := protocol.DecodeJavaScriptJSONString(reasonRaw)
			prefix, _ := protocol.DecodeJavaScriptJSONString(raw("Prompt hook condition was not met: "))
			blocking = protocol.QuoteJavaScriptUTF16(append(prefix, units...))
		}
		return Result{Outcome: "blocking", BlockingError: fmt.Sprintf("Prompt hook condition was not met: %s", reason), BlockingErrorRaw: blocking, Command: prompt, PreventContinuation: true, StopReason: reasonRaw}
	}
	return Result{Outcome: "success", Payload: attachment("hook_success", name, event, id, protocol.JSONField{Name: "content", Value: raw("")})}
}
