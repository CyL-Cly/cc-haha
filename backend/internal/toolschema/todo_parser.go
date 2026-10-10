package toolschema

import (
	"bytes"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func todoInvalidType(expected string, value json.RawMessage, path []any) json.RawMessage {
	issue, _ := parseObject(writeInvalidType(expected, bytes.TrimSpace(value), nil))
	issue.Set("path", array(path))
	return raw(issue)
}

func parseTodoInput(in protocol.JSONObject) (protocol.JSONObject, error) {
	if !in.IsObject() {
		return protocol.JSONObject{}, &ValidationError{Issues: array([]json.RawMessage{todoInvalidType("object", raw(in), []any{})})}
	}
	issues := []json.RawMessage{}
	var todos []json.RawMessage
	value := bytes.TrimSpace(in.Get("todos"))
	if len(value) == 0 || value[0] != '[' || json.Unmarshal(value, &todos) != nil {
		issues = append(issues, todoInvalidType("array", value, []any{"todos"}))
	}
	result := []protocol.JSONObject{}
	for index, value := range todos {
		item, err := parseObject(value)
		if err != nil || !item.IsObject() {
			issues = append(issues, todoInvalidType("object", value, []any{"todos", index}))
			continue
		}
		parsed := protocol.NewJSONObject()
		for _, name := range []string{"content", "status", "activeForm"} {
			path := []any{"todos", index, name}
			value := item.Get(name)
			units, err := protocol.DecodeJavaScriptJSONString(value)
			if name == "status" {
				status := item.String(name)
				if err != nil || status != "pending" && status != "in_progress" && status != "completed" {
					issues = append(issues, raw(protocol.NewJSONObject(protocol.JSONField{Name: "code", Value: quote("invalid_value")}, protocol.JSONField{Name: "values", Value: array([]string{"pending", "in_progress", "completed"})}, protocol.JSONField{Name: "path", Value: array(path)}, protocol.JSONField{Name: "message", Value: quote(`Invalid option: expected one of "pending"|"in_progress"|"completed"`)})))
					continue
				}
			} else {
				if err != nil {
					issues = append(issues, todoInvalidType("string", value, path))
					continue
				}
				if len(units) == 0 {
					message := "Content cannot be empty"
					if name == "activeForm" {
						message = "Active form cannot be empty"
					}
					issues = append(issues, raw(protocol.NewJSONObject(protocol.JSONField{Name: "origin", Value: quote("string")}, protocol.JSONField{Name: "code", Value: quote("too_small")}, protocol.JSONField{Name: "minimum", Value: json.RawMessage("1")}, protocol.JSONField{Name: "inclusive", Value: json.RawMessage("true")}, protocol.JSONField{Name: "path", Value: array(path)}, protocol.JSONField{Name: "message", Value: quote(message)})))
					continue
				}
			}
			parsed.Set(name, value)
		}
		result = append(result, parsed)
	}
	unknown := []json.RawMessage{}
	for _, p := range in.OwnProperties() {
		units, _ := protocol.DecodeJavaScriptJSONString(p.NameJSON)
		if len(units) != 5 || units[0] != 't' || units[1] != 'o' || units[2] != 'd' || units[3] != 'o' || units[4] != 's' {
			unknown = append(unknown, p.NameJSON)
		}
	}
	if len(unknown) != 0 {
		word := "key"
		if len(unknown) > 1 {
			word = "keys"
		}
		message, _ := protocol.DecodeJavaScriptJSONString(quote("Unrecognized " + word + ": "))
		for index, name := range unknown {
			if index > 0 {
				message = append(message, ',', ' ')
			}
			units, _ := protocol.DecodeJavaScriptJSONString(name)
			message = append(message, '"')
			message = append(message, units...)
			message = append(message, '"')
		}
		issues = append(issues, raw(protocol.NewJSONObject(protocol.JSONField{Name: "code", Value: quote("unrecognized_keys")}, protocol.JSONField{Name: "keys", Value: array(unknown)}, protocol.JSONField{Name: "path", Value: json.RawMessage("[]")}, protocol.JSONField{Name: "message", Value: protocol.QuoteJavaScriptUTF16(message)})))
	}
	if len(issues) > 0 {
		return protocol.JSONObject{}, &ValidationError{Issues: array(issues)}
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "todos", Value: array(result)}), nil
}
