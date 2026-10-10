package clisettings

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var nativePluginKeys = []string{"enabledPlugins", "extraKnownMarketplaces", "strictKnownMarketplaces", "blockedMarketplaces", "agent"}

// Literal SettingsSchema field declarations reuse the complete native
// MarketplaceSource schema. No runtime values or Source parse output define
// the parser; the existing finite profile retains its original admission.
var nativePluginSchema = sync.OnceValues(func() (*pluginschema.Node, error) {
	declarations, err := pluginschema.Declarations()
	if err != nil {
		return nil, err
	}
	var source pluginschema.Node
	if err = json.Unmarshal(declarations.Known.ValueType.Shape.Get("source"), &source); err != nil {
		return nil, err
	}
	text, boolean := &pluginschema.Node{Type: "string"}, &pluginschema.Node{Type: "boolean"}
	optional := func(n *pluginschema.Node) *pluginschema.Node {
		return &pluginschema.Node{Type: "optional", InnerType: n}
	}
	array := func(n *pluginschema.Node) *pluginschema.Node { return &pluginschema.Node{Type: "array", Element: n} }
	record := func(n *pluginschema.Node) *pluginschema.Node {
		return &pluginschema.Node{Type: "record", KeyType: text, ValueType: n}
	}
	shape := func(fields ...protocol.JSONField) *pluginschema.Node {
		return &pluginschema.Node{Type: "object", Shape: protocol.NewJSONObject(fields...)}
	}
	field := func(name string, n *pluginschema.Node) protocol.JSONField {
		raw, _ := json.Marshal(n)
		return protocol.JSONField{Name: name, Value: raw}
	}
	extra := shape(field("source", &source), field("installLocation", optional(text)), field("autoUpdate", optional(boolean)))
	return shape(
		field("enabledPlugins", optional(record(&pluginschema.Node{Type: "union", Options: []*pluginschema.Node{array(text), boolean, {Type: "undefined"}}}))),
		field("extraKnownMarketplaces", optional(record(extra))),
		field("strictKnownMarketplaces", optional(array(&source))),
		field("blockedMarketplaces", optional(array(&source))),
		field("agent", optional(text)),
	), nil
})

func parseNativePluginFields(input protocol.JSONObject, file string) (*protocol.JSONObject, []json.RawMessage, error) {
	schema, err := nativePluginSchema()
	if err != nil {
		return nil, nil, err
	}
	result := protocol.NewJSONObject()
	errors := []json.RawMessage{}
	for _, key := range nativePluginKeys {
		var field pluginschema.Node
		if err := json.Unmarshal(schema.Shape.Get(key), &field); err != nil {
			return nil, nil, err
		}
		parsed, issues := pluginschema.Validate(&field, input.Get(key))
		result.Set(key, parsed)
		aborted := false
		for _, issue := range issues {
			issue.Path = append([]any{key}, issue.Path...)
			errors = append(errors, formatNativePluginIssue(file, issue))
			aborted = aborted || issue.Aborted
		}
		// Zod runs the record-level name check after continuable refinements,
		// but skips it after an aborted child parse. Unrelated fields do not
		// suppress that check and do not move it after their own errors.
		if key == "extraKnownMarketplaces" && !aborted {
			errors = append(errors, extraMarketplaceNameErrors(input.Get(key), file)...)
		}
	}
	return &result, errors, nil
}

func formatNativePluginIssue(file string, issue pluginschema.Issue) json.RawMessage {
	parts := make([]string, len(issue.Path))
	for i, part := range issue.Path {
		parts[i] = fmt.Sprint(part)
	}
	path := strings.Join(parts, ".")
	code := issue.Details.String("code")
	if code == "invalid_type" {
		expected := issue.Details.String("expected")
		received := strings.TrimPrefix(issue.Message, "Invalid input: expected "+expected+", received ")
		return validationError(file, path, expected, received)
	}
	message := issue.Message
	out := protocol.NewJSONObject(protocol.JSONField{Name: "file", Value: quoted(file)}, protocol.JSONField{Name: "path", Value: quoted(path)})
	if code == "too_small" {
		minimum := string(issue.Details.Get("minimum"))
		message = "Number must be greater than or equal to " + minimum
		out.Set("message", quoted(message))
		out.Set("expected", quoted(minimum))
	}
	out.Set("message", quoted(message))
	encoded, _ := out.MarshalJSON()
	return encoded
}

func extraMarketplaceNameErrors(extra json.RawMessage, file string) []json.RawMessage {
	errors := []json.RawMessage{}
	if extra != nil {
		var marketplaces protocol.JSONObject
		_ = marketplaces.UnmarshalJSON(extra)
		for _, property := range marketplaces.OwnProperties() {
			var key string
			_ = json.Unmarshal(property.NameJSON, &key)
			if key == "__proto__" {
				continue
			}
			var entry, source protocol.JSONObject
			_ = entry.UnmarshalJSON(property.Value)
			_ = source.UnmarshalJSON(entry.Get("source"))
			if source.String("source") == "settings" && source.String("name") != key {
				out := protocol.NewJSONObject(protocol.JSONField{Name: "file", Value: quoted(file)}, protocol.JSONField{Name: "path", Value: quoted("extraKnownMarketplaces." + key + ".source.name")}, protocol.JSONField{Name: "message", Value: quoted(fmt.Sprintf("Settings-sourced marketplace name must match its extraKnownMarketplaces key (got key \"%s\" but source.name \"%s\")", key, source.String("name")))})
				encoded, _ := out.MarshalJSON()
				errors = append(errors, encoded)
			}
		}
	}
	return errors
}
