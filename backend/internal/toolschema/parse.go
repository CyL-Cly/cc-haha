package toolschema

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// ObjectInputValidationError is the original object schema's root issue. The
// ordered object execution boundary cannot contain an array or primitive, but
// those valid provider JSON values still need model-visible schema feedback.
func ObjectInputValidationError(input json.RawMessage) error {
	return &ValidationError{Issues: array([]json.RawMessage{writeInvalidType("object", bytes.TrimSpace(input), []string{})})}
}

func ParseInputJSON(definition Definition, input json.RawMessage) (protocol.JSONObject, error) {
	var object protocol.JSONObject
	if err := json.Unmarshal(input, &object); err != nil || !object.IsObject() {
		return protocol.JSONObject{}, ObjectInputValidationError(input)
	}
	return ParseInput(definition, object)
}

// ParseInput is the execution boundary for the original registered object of
// strings. Request normalization must not call it: Source preserves tool_use
// input independently from the parsed input passed to a tool's handler.
func ParseInput(definition Definition, input protocol.JSONObject) (protocol.JSONObject, error) {
	if definition.Kind == "builtin" && isTeamTool(definition.Name) {
		return parseTeamInput(definition, input)
	}
	if definition.Kind == "builtin" && definition.Name == "SendMessage" {
		return parseSendMessageInput(definition, input)
	}
	if definition.Kind == "builtin" && (definition.Name == "WebFetch" || definition.Name == "WebSearch") {
		return parseWebInput(definition, input)
	}
	if definition.Kind == "builtin" && definition.Name == "Write" {
		return parseWriteInput(input)
	}
	if definition.Kind == "builtin" && definition.Name == "TodoWrite" {
		return parseTodoInput(input)
	}
	if definition.Kind == "builtin" && definition.Name == "AskUserQuestion" {
		return parseQuestionInput(definition, input)
	}
	if definition.Kind == "builtin" && (definition.Name == "EnterPlanMode" || definition.Name == "ExitPlanMode") {
		return parsePlanInput(definition, input)
	}
	if definition.Kind == "builtin" && isTaskDefinition(definition.Name) {
		return parseTaskInput(definition, input)
	}
	if definition.Kind == "builtin" && (definition.Name == "EnterWorktree" || definition.Name == "ExitWorktree") {
		return parseWorktreeInput(definition, input)
	}
	if definition.Kind == "builtin" && definition.InputSchema != nil {
		return parseNativeInput(definition, input)
	}
	if definition.Kind != "registered" || definition.InputSchema == nil || definition.InputSchema.Kind != "object" {
		return protocol.JSONObject{}, unsupported("execution parser for native builtin schema")
	}
	schema := definition.InputSchema
	for _, field := range schema.Fields {
		if field.Schema.Kind != "string" || len(field.Schema.Default) > 0 || field.Schema.Semantic {
			return protocol.JSONObject{}, unsupported("execution parser for non-string registered field")
		}
	}
	if schema.UnknownKeys == "strict" || schema.UnknownKeys == "passthrough" {
		return protocol.JSONObject{}, unsupported("execution parser for registered non-strip policy")
	}
	if !input.IsObject() {
		return protocol.JSONObject{}, unsupported("execution parser requires an object input")
	}
	output := protocol.NewJSONObject()
	issues := []json.RawMessage{}
	for _, field := range shapeFields(schema.Fields) {
		value := input.Get(field.Name)
		if value == nil && field.Schema.Optional {
			continue
		}
		if _, err := protocol.DecodeJavaScriptJSONString(value); err != nil {
			received := "undefined"
			if len(value) > 0 {
				switch value[0] {
				case 'n':
					received = "null"
				case 't', 'f':
					received = "boolean"
				case '[':
					received = "array"
				case '{':
					received = "object"
				default:
					received = "number"
				}
			}
			issue := protocol.NewJSONObject(
				protocol.JSONField{Name: "expected", Value: quote("string")},
				protocol.JSONField{Name: "code", Value: quote("invalid_type")},
				protocol.JSONField{Name: "path", Value: array([]string{field.Name})},
				protocol.JSONField{Name: "message", Value: quote(fmt.Sprintf("Invalid input: expected string, received %s", received))},
			)
			issues = append(issues, raw(issue))
			continue
		}
		output.Set(field.Name, value)
	}
	if len(issues) > 0 {
		return protocol.JSONObject{}, &ValidationError{Issues: array(issues)}
	}
	return output, nil
}
