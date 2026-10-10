package clisettings

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Source retains these retired settings for old-file compatibility. They are
// validated and returned by SettingsSchema; no commit/PR attribution is added.
func parseNativeDeprecatedAttribution(input protocol.JSONObject, file string) (protocol.JSONObject, []json.RawMessage) {
	fields := protocol.NewJSONObject()
	errors := []json.RawMessage{}
	if value := input.Get("attribution"); value != nil {
		if receivedType(value) != "object" {
			errors = append(errors, validationError(file, "attribution", "object", receivedType(value)))
		} else {
			var object protocol.JSONObject
			_ = object.UnmarshalJSON(value)
			result := protocol.NewJSONObject()
			for _, key := range []string{"commit", "pr"} {
				if field := object.Get(key); field != nil {
					if receivedType(field) != "string" {
						errors = append(errors, validationError(file, "attribution."+key, "string", receivedType(field)))
					} else {
						result.Set(key, field)
					}
				}
			}
			encoded, _ := result.MarshalJSON()
			fields.Set("attribution", encoded)
		}
	}
	return fields, errors
}
