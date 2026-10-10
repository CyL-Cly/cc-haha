package clisettings

import (
	"encoding/json"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var nativeMCPKeys = []string{"allowedMcpServers", "deniedMcpServers", "allowManagedMcpServersOnly"}

func nativeMCPEntry() *pluginschema.Node {
	text := &pluginschema.Node{Type: "string"}
	nameCheck := pluginschema.Check{Kind: "string_format", Format: "regex", Error: "Server name can only contain letters, numbers, hyphens, and underscores"}
	nameCheck.Pattern.Regex = "^[a-zA-Z0-9_-]+$"
	name := &pluginschema.Node{Type: "string", Checks: []pluginschema.Check{nameCheck}}
	command := &pluginschema.Node{Type: "array", Element: text, Checks: []pluginschema.Check{{Kind: "min_length", Minimum: 1, Error: "Server command must have at least one element (the command)"}}}
	optional := func(node *pluginschema.Node) json.RawMessage {
		value, _ := json.Marshal(&pluginschema.Node{Type: "optional", InnerType: node})
		return value
	}
	return &pluginschema.Node{Type: "object", Shape: protocol.NewJSONObject(protocol.JSONField{Name: "serverName", Value: optional(name)}, protocol.JSONField{Name: "serverCommand", Value: optional(command)}, protocol.JSONField{Name: "serverUrl", Value: optional(text)})}
}

func parseNativeMCPFields(input protocol.JSONObject, file string) (*protocol.JSONObject, []json.RawMessage) {
	result := protocol.NewJSONObject()
	failures := []json.RawMessage{}
	for _, key := range nativeMCPKeys {
		value := input.Get(key)
		if value == nil {
			continue
		}
		if key == "allowManagedMcpServersOnly" {
			if receivedType(value) != "boolean" {
				failures = append(failures, validationError(file, key, "boolean", receivedType(value)))
			} else {
				result.Set(key, value)
			}
			continue
		}
		if receivedType(value) != "array" {
			failures = append(failures, validationError(file, key, "array", receivedType(value)))
			continue
		}
		var entries []json.RawMessage
		_ = json.Unmarshal(value, &entries)
		parsed := []json.RawMessage{}
		for i, entry := range entries {
			out, issues := pluginschema.Validate(nativeMCPEntry(), entry)
			aborted := false
			for _, issue := range issues {
				issue.Path = append([]any{key, i}, issue.Path...)
				failures = append(failures, formatNativePluginIssue(file, issue))
				aborted = aborted || issue.Aborted
			}
			if !aborted {
				var raw protocol.JSONObject
				_ = raw.UnmarshalJSON(entry)
				defined := 0
				for _, field := range []string{"serverName", "serverCommand", "serverUrl"} {
					if raw.Get(field) != nil {
						defined++
					}
				}
				if defined != 1 {
					failure := protocol.NewJSONObject(protocol.JSONField{Name: "file", Value: quoted(file)}, protocol.JSONField{Name: "path", Value: quoted(key + "." + strconv.Itoa(i))}, protocol.JSONField{Name: "message", Value: quoted(`Entry must have exactly one of "serverName", "serverCommand", or "serverUrl"`)})
					encoded, _ := failure.MarshalJSON()
					failures = append(failures, encoded)
				}
			}
			parsed = append(parsed, out)
		}
		encoded, _ := json.Marshal(parsed)
		result.Set(key, encoded)
	}
	return &result, failures
}
