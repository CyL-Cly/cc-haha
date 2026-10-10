package clisettings

import (
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// This is the original optional SettingsSchema webSearch object, consumed by
// native tool metadata. Executing WebSearch still requires its actual handler.
func parseNativeWebSearch(value json.RawMessage, file string) (json.RawMessage, []json.RawMessage) {
	optional := func(node *pluginschema.Node) *pluginschema.Node {
		return &pluginschema.Node{Type: "optional", InnerType: node}
	}
	field := func(name string, node *pluginschema.Node) protocol.JSONField {
		raw, _ := json.Marshal(node)
		return protocol.JSONField{Name: name, Value: raw}
	}
	entries := protocol.NewJSONObject()
	for _, mode := range []string{"auto", "anthropic", "tavily", "brave", "disabled"} {
		entries.Set(mode, quoted(mode))
	}
	shape := protocol.NewJSONObject(
		field("mode", optional(&pluginschema.Node{Type: "enum", Entries: entries})),
		field("tavilyApiKey", optional(&pluginschema.Node{Type: "string"})),
		field("braveApiKey", optional(&pluginschema.Node{Type: "string"})),
	)
	parsed, issues := pluginschema.Validate(optional(&pluginschema.Node{Type: "object", Shape: shape}), value)
	errors := []json.RawMessage{}
	for _, issue := range issues {
		issue.Path = append([]any{"webSearch"}, issue.Path...)
		if issue.Details.String("code") == "invalid_value" {
			values := []string{}
			var rawValues []json.RawMessage
			_ = json.Unmarshal(issue.Details.Get("values"), &rawValues)
			for _, value := range rawValues {
				values = append(values, string(value))
			}
			expected := strings.Join(values, ", ")
			path := "webSearch.mode"
			out := protocol.NewJSONObject(protocol.JSONField{Name: "file", Value: quoted(file)}, protocol.JSONField{Name: "path", Value: quoted(path)}, protocol.JSONField{Name: "message", Value: quoted("Invalid value. Expected one of: " + expected)}, protocol.JSONField{Name: "expected", Value: quoted(expected)}, protocol.JSONField{Name: "suggestion", Value: quoted("Valid values: " + expected)})
			raw, _ := out.MarshalJSON()
			errors = append(errors, raw)
			continue
		}
		errors = append(errors, formatNativePluginIssue(file, issue))
	}
	return parsed, errors
}
