package toolschema

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Team schemas use ordinary strip objects at every proposed-plan level. The
// leaf parser is shared with the existing Zod primitive recipes so safe-int,
// UTF-16 bounds and issue ordering stay identical.
func parseTeamInput(def Definition, in protocol.JSONObject) (protocol.JSONObject, error) {
	parsed, issues, err := parseTeamNode(*def.InputSchema, raw(in), []any{})
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if len(issues) > 0 {
		return protocol.JSONObject{}, &ValidationError{Issues: array(issues)}
	}
	return parseObject(parsed)
}
func parseTeamNode(schema Schema, value json.RawMessage, path []any) (json.RawMessage, []json.RawMessage, error) {
	if value == nil {
		if schema.Default != nil {
			return schema.Default, nil, nil
		}
		if schema.Optional {
			return nil, nil, nil
		}
	}
	if schema.Kind == "array" {
		var items []json.RawMessage
		if !bytes.HasPrefix(bytes.TrimSpace(value), []byte("[")) || json.Unmarshal(value, &items) != nil {
			return nil, []json.RawMessage{todoInvalidType("array", value, path)}, nil
		}
		out := []json.RawMessage{}
		issues := []json.RawMessage{}
		for i, item := range items {
			child, diags, err := parseTeamNode(*schema.Items, item, append(append([]any{}, path...), i))
			if err != nil {
				return nil, nil, err
			}
			out = append(out, child)
			issues = append(issues, diags...)
		}
		return array(out), issues, nil
	}
	if schema.Kind == "object" {
		object, err := parseObject(value)
		if err != nil || !object.IsObject() {
			return nil, []json.RawMessage{todoInvalidType("object", value, path)}, nil
		}
		out := protocol.NewJSONObject()
		issues := []json.RawMessage{}
		known := []Field{}
		for _, field := range shapeFields(schema.Fields) {
			child, diags, err := parseTeamNode(field.Schema, object.Get(field.Name), append(append([]any{}, path...), field.Name))
			if err != nil {
				return nil, nil, err
			}
			out.Set(field.Name, child)
			issues = append(issues, diags...)
			known = append(known, Field{Name: field.Name, Schema: Schema{Kind: "unknown", Native: true, Optional: true}})
		}
		if schema.UnknownKeys == "strict" {
			_, err := parseNativeInput(Definition{InputSchema: &Schema{Kind: "object", UnknownKeys: "strict", Fields: known}}, object)
			if validation, ok := err.(*ValidationError); ok {
				var extra []json.RawMessage
				_ = json.Unmarshal(validation.Issues, &extra)
				for _, issue := range extra {
					v, _ := parseObject(issue)
					v.Set("path", array(path))
					issues = append(issues, raw(v))
				}
			} else if err != nil {
				return nil, nil, err
			}
		}
		return raw(out), issues, nil
	}
	parsed, err := parseNativeInput(Definition{InputSchema: &Schema{Kind: "object", UnknownKeys: "strip", Fields: []Field{{Name: "value", Schema: schema}}}}, protocol.NewJSONObject(protocol.JSONField{Name: "value", Value: value}))
	if err == nil {
		return parsed.Get("value"), nil, nil
	}
	validation, ok := err.(*ValidationError)
	if !ok {
		return nil, nil, err
	}
	var issues []json.RawMessage
	_ = json.Unmarshal(validation.Issues, &issues)
	for i, issue := range issues {
		v, _ := parseObject(issue)
		v.Set("path", array(path))
		issues[i] = raw(v)
	}
	return nil, issues, nil
}
