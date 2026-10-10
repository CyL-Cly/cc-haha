package sdkhost

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func systemContextStrings(value protocol.JSONObject) map[string]string {
	result := map[string]string{}
	for _, field := range value.OwnProperties() {
		var key, text string
		if json.Unmarshal(field.NameJSON, &key) == nil && json.Unmarshal(field.Value, &text) == nil {
			result[key] = text
		}
	}
	return result
}
