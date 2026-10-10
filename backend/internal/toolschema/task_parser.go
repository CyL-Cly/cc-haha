package toolschema

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

func isTaskDefinition(name string) bool {
	return name == "TaskCreate" || name == "TaskGet" || name == "TaskUpdate" || name == "TaskList"
}
func taskFields(name string) []Field {
	record := func(description string) Schema {
		return Schema{Kind: "record", Native: true, Optional: true, Description: quote(description)}
	}
	spinner := `Present continuous form shown in spinner when in_progress (e.g., "Running tests")`
	stringItem := Schema{Kind: "string"}
	arrayOfStrings := func(description string) Schema {
		return Schema{Kind: "array", Native: true, Items: &stringItem, Optional: true, Description: quote(description)}
	}
	switch name {
	case "TaskCreate":
		return []Field{{"subject", textSchema("A brief title for the task", false)}, {"description", textSchema("What needs to be done", false)}, {"activeForm", textSchema(spinner, true)}, {"metadata", record("Arbitrary metadata to attach to the task")}}
	case "TaskGet":
		return []Field{{"taskId", textSchema("The ID of the task to retrieve", false)}}
	case "TaskUpdate":
		return []Field{{"taskId", textSchema("The ID of the task to update", false)}, {"subject", textSchema("New subject for the task", true)}, {"description", textSchema("New description for the task", true)}, {"activeForm", textSchema(spinner, true)}, {"status", Schema{Kind: "union", Native: true, Optional: true, Description: quote("New status for the task"), Members: []Schema{{Kind: "enum", Values: []string{"pending", "in_progress", "completed"}}, {Kind: "literal", Native: true, Literal: quote("deleted")}}}}, {"addBlocks", arrayOfStrings("Task IDs that this task blocks")}, {"addBlockedBy", arrayOfStrings("Task IDs that block this task")}, {"owner", textSchema("New owner for the task", true)}, {"metadata", record("Metadata keys to merge into the task. Set a key to null to delete it.")}}
	}
	return []Field{}
}
func taskEnumIssue(values []string) json.RawMessage {
	choices := []string{}
	for _, value := range values {
		choices = append(choices, string(quote(value)))
	}
	message := "Invalid option: expected one of " + strings.Join(choices, "|")
	if len(values) == 1 {
		message = "Invalid input: expected " + choices[0]
	}
	return raw(protocol.NewJSONObject(protocol.JSONField{Name: "code", Value: quote("invalid_value")}, protocol.JSONField{Name: "values", Value: array(values)}, protocol.JSONField{Name: "path", Value: json.RawMessage("[]")}, protocol.JSONField{Name: "message", Value: quote(message)}))
}
func parseTaskInput(definition Definition, in protocol.JSONObject) (protocol.JSONObject, error) {
	out := protocol.NewJSONObject()
	issues := []json.RawMessage{}
	known := map[string]bool{}
	for _, f := range definition.InputSchema.Fields {
		known[f.Name] = true
		value := bytes.TrimSpace(in.Get(f.Name))
		if value == nil && f.Schema.Optional {
			continue
		}
		path := []any{f.Name}
		switch f.Schema.Kind {
		case "string":
			if _, err := protocol.DecodeJavaScriptJSONString(value); err != nil {
				issues = append(issues, todoInvalidType("string", value, path))
				continue
			}
		case "record":
			var record protocol.JSONObject
			if record.UnmarshalJSON(value) != nil || !record.IsObject() {
				issues = append(issues, todoInvalidType("record", value, path))
				continue
			}
			// Zod's record parser creates an ordinary object and deliberately excludes __proto__.
			record.Delete("__proto__")
			value = raw(record)
		case "array":
			var items []json.RawMessage
			if len(value) == 0 || value[0] != '[' || json.Unmarshal(value, &items) != nil {
				issues = append(issues, todoInvalidType("array", value, path))
				continue
			}
			for i, item := range items {
				if _, err := protocol.DecodeJavaScriptJSONString(item); err != nil {
					issues = append(issues, todoInvalidType("string", item, []any{f.Name, i}))
				}
			}
		case "union":
			status, err := protocol.DecodeJavaScriptJSONString(value)
			valid := false
			if err == nil {
				for _, candidate := range []string{"pending", "in_progress", "completed", "deleted"} {
					units, _ := protocol.DecodeJavaScriptJSONString(quote(candidate))
					if len(units) == len(status) && equalTaskUnits(units, status) {
						valid = true
					}
				}
			}
			if !valid {
				issues = append(issues, raw(protocol.NewJSONObject(protocol.JSONField{Name: "code", Value: quote("invalid_union")}, protocol.JSONField{Name: "errors", Value: array([][]json.RawMessage{{taskEnumIssue([]string{"pending", "in_progress", "completed"})}, {taskEnumIssue([]string{"deleted"})}})}, protocol.JSONField{Name: "path", Value: array(path)}, protocol.JSONField{Name: "message", Value: quote("Invalid input")})))
				continue
			}
		}
		out.Set(f.Name, value)
	}
	unknown := []json.RawMessage{}
	for _, property := range in.OwnProperties() {
		name, _ := protocol.DecodeJavaScriptJSONString(property.NameJSON)
		found := false
		for key := range known {
			units, _ := protocol.DecodeJavaScriptJSONString(quote(key))
			if equalTaskUnits(name, units) {
				found = true
				break
			}
		}
		if !found {
			unknown = append(unknown, property.NameJSON)
		}
	}
	if len(unknown) > 0 {
		word := "key"
		if len(unknown) > 1 {
			word = "keys"
		}
		message, _ := protocol.DecodeJavaScriptJSONString(quote("Unrecognized " + word + ": "))
		for i, key := range unknown {
			if i > 0 {
				message = append(message, ',', ' ')
			}
			units, _ := protocol.DecodeJavaScriptJSONString(key)
			message = append(message, '"')
			message = append(message, units...)
			message = append(message, '"')
		}
		issues = append(issues, raw(protocol.NewJSONObject(protocol.JSONField{Name: "code", Value: quote("unrecognized_keys")}, protocol.JSONField{Name: "keys", Value: array(unknown)}, protocol.JSONField{Name: "path", Value: json.RawMessage("[]")}, protocol.JSONField{Name: "message", Value: protocol.QuoteJavaScriptUTF16(message)})))
	}
	if len(issues) > 0 {
		return protocol.JSONObject{}, &ValidationError{Issues: array(issues)}
	}
	return out, nil
}
func equalTaskUnits(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
