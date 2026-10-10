package clisettings

import (
	"encoding/json"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var nativeHTTPHookKeys = []string{"allowedHttpHookUrls", "httpHookAllowedEnvVars"}

func nativeHTTPHookErrors(input protocol.JSONObject, file string) []json.RawMessage {
	issues := []json.RawMessage{}
	for _, key := range nativeHTTPHookKeys {
		value := input.Get(key)
		if value == nil {
			continue
		}
		if receivedType(value) != "array" {
			issues = append(issues, validationError(file, key, "array", receivedType(value)))
			continue
		}
		var entries []json.RawMessage
		_ = json.Unmarshal(value, &entries)
		for i, entry := range entries {
			if receivedType(entry) != "string" {
				issues = append(issues, validationError(file, key+"."+strconv.Itoa(i), "string", receivedType(entry)))
			}
		}
	}
	return issues
}
