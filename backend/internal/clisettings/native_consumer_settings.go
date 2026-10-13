package clisettings

import (
	"encoding/json"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var nativeConsumerKeys = []string{"claudeMdExcludes", "plansDirectory", "spinnerVerbs", "showThinkingSummaries", "autoDreamEnabled"}

func parseNativeConsumerSettings(input protocol.JSONObject, file string) (protocol.JSONObject, []json.RawMessage) {
	out := protocol.NewJSONObject()
	errors := []json.RawMessage{}
	stringsArray := func(value json.RawMessage, path string) {
		if receivedType(value) != "array" {
			errors = append(errors, validationError(file, path, "array", receivedType(value)))
			return
		}
		var entries []json.RawMessage
		_ = json.Unmarshal(value, &entries)
		for index, entry := range entries {
			if receivedType(entry) != "string" {
				errors = append(errors, validationError(file, path+"."+strconv.Itoa(index), "string", receivedType(entry)))
			}
		}
	}
	if value := input.Get("claudeMdExcludes"); value != nil {
		stringsArray(value, "claudeMdExcludes")
		out.Set("claudeMdExcludes", value)
	}
	if value := input.Get("plansDirectory"); value != nil {
		if receivedType(value) != "string" {
			errors = append(errors, validationError(file, "plansDirectory", "string", receivedType(value)))
		}
		out.Set("plansDirectory", value)
	}
	if value := input.Get("spinnerVerbs"); value != nil {
		if receivedType(value) != "object" {
			errors = append(errors, validationError(file, "spinnerVerbs", "object", receivedType(value)))
		} else {
			var config protocol.JSONObject
			_ = config.UnmarshalJSON(value)
			var mode []byte
			if value := config.Get("mode"); value != nil {
				mode = protocol.StringifyValidatedJavaScriptJSON(value)
			}
			if string(mode) != `"append"` && string(mode) != `"replace"` {
				var issue protocol.JSONObject
				_ = issue.UnmarshalJSON(enumError(file, "spinnerVerbs.mode", []string{"append", "replace"}))
				issue.Delete("docLink")
				encoded, _ := issue.MarshalJSON()
				errors = append(errors, encoded)
			}
			stringsArray(config.Get("verbs"), "spinnerVerbs.verbs")
			parsed := protocol.NewJSONObject()
			parsed.Set("mode", config.Get("mode"))
			parsed.Set("verbs", config.Get("verbs"))
			encoded, _ := parsed.MarshalJSON()
			out.Set("spinnerVerbs", encoded)
		}
	}
	if value := input.Get("showThinkingSummaries"); value != nil {
		if receivedType(value) != "boolean" {
			errors = append(errors, validationError(file, "showThinkingSummaries", "boolean", receivedType(value)))
		}
		out.Set("showThinkingSummaries", value)
	}
	if value := input.Get("autoDreamEnabled"); value != nil {
		if receivedType(value) != "boolean" {
			errors = append(errors, validationError(file, "autoDreamEnabled", "boolean", receivedType(value)))
		}
		out.Set("autoDreamEnabled", value)
	}
	return out, errors
}
