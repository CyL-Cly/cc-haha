package clisettings

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"math"
	"strconv"
)

// pluginConfigs is the literal SettingsSchema record recipe. This admits
// primitive plugin options, not plugin discovery, secure storage or handlers.
func parsePluginConfigs(input protocol.JSONObject, out *protocol.JSONObject, result *ParsedSettings, file string) {
	raw := input.Get("pluginConfigs")
	if raw == nil {
		return
	}
	if receivedType(raw) != "object" {
		result.Errors = append(result.Errors, validationError(file, "pluginConfigs", "record", receivedType(raw)))
		return
	}
	var plugins protocol.JSONObject
	_ = plugins.UnmarshalJSON(raw)
	next := protocol.NewJSONObject()
	for _, plugin := range plugins.OwnProperties() {
		path := appendPath(quoted("pluginConfigs"), plugin.NameJSON)
		if receivedType(plugin.Value) != "object" {
			result.Errors = append(result.Errors, pluginTypeError(file, path, "object", receivedType(plugin.Value)))
			continue
		}
		var config protocol.JSONObject
		_ = config.UnmarshalJSON(plugin.Value)
		parsed := protocol.NewJSONObject()
		for _, name := range []string{"mcpServers", "options"} {
			value := config.Get(name)
			if value == nil {
				continue
			}
			optionPath := appendPath(path, quoted(name))
			if receivedType(value) != "object" {
				result.Errors = append(result.Errors, pluginTypeError(file, optionPath, "record", receivedType(value)))
				continue
			}
			var fields protocol.JSONObject
			_ = fields.UnmarshalJSON(value)
			if name == "options" {
				parsePluginValues(fields, result, file, optionPath)
				encoded, _ := fields.MarshalJSON()
				parsed.Set(name, encoded)
				continue
			}
			for _, server := range fields.OwnProperties() {
				serverPath := appendPath(optionPath, server.NameJSON)
				if receivedType(server.Value) != "object" {
					result.Errors = append(result.Errors, pluginTypeError(file, serverPath, "record", receivedType(server.Value)))
					continue
				}
				var options protocol.JSONObject
				_ = options.UnmarshalJSON(server.Value)
				parsePluginValues(options, result, file, serverPath)
			}
			encoded, _ := fields.MarshalJSON()
			parsed.Set(name, encoded)
		}
		encoded, _ := parsed.MarshalJSON()
		_ = next.SetPropertyJSON(plugin.NameJSON, encoded)
	}
	encoded, _ := next.MarshalJSON()
	out.Set("pluginConfigs", encoded)
}
func appendPath(prefix, key json.RawMessage) json.RawMessage {
	a, _ := protocol.DecodeJavaScriptJSONString(prefix)
	b, _ := protocol.DecodeJavaScriptJSONString(key)
	a = append(a, '.')
	return protocol.QuoteJavaScriptUTF16(append(a, b...))
}
func pluginTypeError(file string, path json.RawMessage, expected, received string) json.RawMessage {
	var out protocol.JSONObject
	_ = out.UnmarshalJSON(validationError(file, "pluginConfigs.placeholder", expected, received))
	out.Set("path", path)
	raw, _ := out.MarshalJSON()
	return raw
}
func parsePluginValues(object protocol.JSONObject, result *ParsedSettings, file string, path json.RawMessage) {
	for _, property := range object.OwnProperties() {
		raw := property.Value
		valid := receivedType(raw) == "string" || receivedType(raw) == "boolean"
		if receivedType(raw) == "number" {
			number, _ := strconv.ParseFloat(string(raw), 64)
			valid = !math.IsInf(number, 0) && !math.IsNaN(number)
		}
		if receivedType(raw) == "array" {
			var values []json.RawMessage
			_ = json.Unmarshal(raw, &values)
			valid = true
			for _, value := range values {
				if receivedType(value) != "string" {
					valid = false
				}
			}
		}
		if !valid {
			diagnostic := protocol.NewJSONObject(protocol.JSONField{Name: "file", Value: quoted(file)}, protocol.JSONField{Name: "path", Value: appendPath(path, property.NameJSON)}, protocol.JSONField{Name: "message", Value: quoted("Invalid input")})
			encoded, _ := diagnostic.MarshalJSON()
			result.Errors = append(result.Errors, encoded)
		}
	}
}
func processedPluginKey(key json.RawMessage) bool { return bytes.Equal(key, quoted("pluginConfigs")) }
