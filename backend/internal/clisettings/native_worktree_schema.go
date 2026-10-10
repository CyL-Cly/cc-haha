package clisettings

import (
	"encoding/json"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// SettingsSchema's optional worktree object strips unknown nested keys and
// preserves the original raw strings (including lone UTF-16 surrogates).
func parseNativeWorktree(input json.RawMessage, file string) (json.RawMessage, []json.RawMessage) {
	if input == nil {
		return nil, nil
	}
	if receivedType(input) != "object" {
		return nil, []json.RawMessage{validationError(file, "worktree", "object", receivedType(input))}
	}
	var object protocol.JSONObject
	_ = object.UnmarshalJSON(input)
	out := protocol.NewJSONObject()
	errors := []json.RawMessage{}
	for _, key := range []string{"symlinkDirectories", "sparsePaths"} {
		value := object.Get(key)
		if value == nil {
			continue
		}
		if receivedType(value) != "array" {
			errors = append(errors, validationError(file, "worktree."+key, "array", receivedType(value)))
			continue
		}
		var entries []json.RawMessage
		_ = json.Unmarshal(value, &entries)
		for index, entry := range entries {
			if receivedType(entry) != "string" {
				errors = append(errors, validationError(file, "worktree."+key+"."+strconv.Itoa(index), "string", receivedType(entry)))
			}
		}
		out.Set(key, value)
	}
	value, _ := out.MarshalJSON()
	return value, errors
}
