package toolschema

import (
	"bytes"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func parsePlanInput(definition Definition, in protocol.JSONObject) (protocol.JSONObject, error) {
	if definition.Name == "EnterPlanMode" {
		return parseNativeInput(definition, in)
	}
	out := protocol.NewJSONObject()
	issues := []json.RawMessage{}
	if !in.IsObject() {
		return out, &ValidationError{Issues: array([]json.RawMessage{todoInvalidType("object", raw(in), []any{})})}
	}
	if value := in.Get("allowedPrompts"); value != nil {
		var items []json.RawMessage
		if bytes.TrimSpace(value)[0] != '[' || json.Unmarshal(value, &items) != nil {
			issues = append(issues, todoInvalidType("array", value, []any{"allowedPrompts"}))
		} else {
			parsed := []protocol.JSONObject{}
			for i, item := range items {
				var object protocol.JSONObject
				if object.UnmarshalJSON(item) != nil || !object.IsObject() {
					issues = append(issues, todoInvalidType("object", item, []any{"allowedPrompts", i}))
					continue
				}
				entry := protocol.NewJSONObject()
				if object.String("tool") != "Bash" {
					issue := protocol.NewJSONObject(protocol.JSONField{Name: "code", Value: quote("invalid_value")}, protocol.JSONField{Name: "values", Value: array([]string{"Bash"})}, protocol.JSONField{Name: "path", Value: array([]any{"allowedPrompts", i, "tool"})}, protocol.JSONField{Name: "message", Value: quote(`Invalid input: expected "Bash"`)})
					issues = append(issues, raw(issue))
				} else {
					entry.Set("tool", quote("Bash"))
				}
				if prompt := object.Get("prompt"); prompt != nil {
					if _, e := protocol.DecodeJavaScriptJSONString(prompt); e != nil {
						issues = append(issues, todoInvalidType("string", prompt, []any{"allowedPrompts", i, "prompt"}))
					} else {
						entry.Set("prompt", prompt)
					}
				} else {
					issues = append(issues, todoInvalidType("string", nil, []any{"allowedPrompts", i, "prompt"}))
				}
				parsed = append(parsed, entry)
			}
			out.Set("allowedPrompts", array(parsed))
		}
	}
	for _, p := range in.OwnProperties() {
		var name string
		_ = json.Unmarshal(p.NameJSON, &name)
		if name != "allowedPrompts" && name != "__proto__" {
			if e := out.SetPropertyJSON(p.NameJSON, p.Value); e != nil {
				return out, e
			}
		}
	}
	if len(issues) > 0 {
		return protocol.JSONObject{}, &ValidationError{Issues: array(issues)}
	}
	return out, nil
}
