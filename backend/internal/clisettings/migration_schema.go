package clisettings

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strconv"
	"strings"
)

var migrationKeys = []string{"permissions", "enableAllProjectMcpServers", "enabledMcpjsonServers", "disabledMcpjsonServers", "skipDangerousModePermissionPrompt", "skipAutoPermissionPrompt"}

func processedMigrationKey(raw json.RawMessage) bool {
	for _, key := range migrationKeys {
		if bytes.Equal(raw, quoted(key)) {
			return true
		}
	}
	return false
}
func parseMigrationFields(input protocol.JSONObject, out *protocol.JSONObject, result *ParsedSettings, file string, keys []string) {
	for _, key := range keys {
		raw := input.Get(key)
		if raw == nil {
			continue
		}
		expected := "boolean"
		if key == "enabledMcpjsonServers" || key == "disabledMcpjsonServers" {
			expected = "array"
		}
		if receivedType(raw) != expected {
			result.Errors = append(result.Errors, validationError(file, key, expected, receivedType(raw)))
			continue
		}
		if expected == "array" {
			validateStringArray(raw, result, file, key)
		}
		out.Set(key, raw)
	}
}
func validateStringArray(raw json.RawMessage, result *ParsedSettings, file, path string) {
	var entries []json.RawMessage
	_ = json.Unmarshal(raw, &entries)
	for index, item := range entries {
		if receivedType(item) != "string" {
			result.Errors = append(result.Errors, validationError(file, path+"."+strconv.Itoa(index), "string", receivedType(item)))
		}
	}
}
func enumError(file, path string, values []string) json.RawMessage {
	quotedValues := make([]string, len(values))
	for index, value := range values {
		quotedValues[index] = string(quoted(value))
	}
	expected := strings.Join(quotedValues, ", ")
	suggestion := `Valid values: ` + expected
	doc := "https://code.claude.com/docs/en/iam#configuring-permissions"
	if path == "permissions.defaultMode" {
		suggestion = `Valid modes: "acceptEdits" (ask before file changes), "plan" (analysis only), "bypassPermissions" (auto-accept all), or "default" (standard behavior)`
		doc = "https://code.claude.com/docs/en/iam#permission-modes"
	}
	object := protocol.NewJSONObject(protocol.JSONField{Name: "file", Value: quoted(file)}, protocol.JSONField{Name: "path", Value: quoted(path)}, protocol.JSONField{Name: "message", Value: quoted("Invalid value. Expected one of: " + expected)}, protocol.JSONField{Name: "expected", Value: quoted(expected)}, protocol.JSONField{Name: "suggestion", Value: quoted(suggestion)}, protocol.JSONField{Name: "docLink", Value: quoted(doc)})
	raw, _ := object.MarshalJSON()
	return raw
}
func parsePermissions(input protocol.JSONObject, out *protocol.JSONObject, result *ParsedSettings, file string) {
	raw := input.Get("permissions")
	if raw == nil {
		return
	}
	if receivedType(raw) != "object" {
		result.Errors = append(result.Errors, validationError(file, "permissions", "object", receivedType(raw)))
		return
	}
	var permissions protocol.JSONObject
	_ = permissions.UnmarshalJSON(raw)
	next := protocol.NewJSONObject()
	for _, key := range []string{"defaultMode", "disableBypassPermissionsMode", "disableAutoMode", "additionalDirectories"} {
		value := permissions.Get(key)
		if value == nil {
			continue
		}
		if key == "additionalDirectories" {
			if receivedType(value) != "array" {
				result.Errors = append(result.Errors, validationError(file, "permissions."+key, "array", receivedType(value)))
			} else {
				validateStringArray(value, result, file, "permissions."+key)
			}
			next.Set(key, value)
			continue
		}
		values := []string{"disable"}
		if key == "defaultMode" {
			values = []string{"acceptEdits", "bypassPermissions", "default", "dontAsk", "plan", "auto"}
		}
		canonical := protocol.StringifyValidatedJavaScriptJSON(value)
		valid := false
		for _, allowed := range values {
			if bytes.Equal(canonical, quoted(allowed)) {
				valid = true
			}
		}
		if !valid {
			result.Errors = append(result.Errors, enumError(file, "permissions."+key, values))
		}
		next.Set(key, value)
	}
	for _, property := range permissions.OwnProperties() {
		known := false
		for _, key := range []string{"defaultMode", "disableBypassPermissionsMode", "disableAutoMode", "additionalDirectories"} {
			if bytes.Equal(property.NameJSON, quoted(key)) {
				known = true
			}
		}
		if !known {
			_ = next.SetPropertyJSON(property.NameJSON, property.Value)
		}
	}
	encoded, _ := next.MarshalJSON()
	out.Set("permissions", encoded)
}
