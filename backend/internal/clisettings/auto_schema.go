package clisettings

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func autoSettingsKey(name json.RawMessage) bool {
	for _, k := range []string{"useAutoModeDuringPlan", "autoMode", "disableAutoMode"} {
		if string(name) == string(quoted(k)) {
			return true
		}
	}
	return false
}
func parseAutoSettings(input protocol.JSONObject, out *protocol.JSONObject, r *ParsedSettings, file string) {
	if v := input.Get("useAutoModeDuringPlan"); v != nil {
		if receivedType(v) != "boolean" {
			r.Errors = append(r.Errors, validationError(file, "useAutoModeDuringPlan", "boolean", receivedType(v)))
		} else {
			out.Set("useAutoModeDuringPlan", v)
		}
	}
	if v := input.Get("autoMode"); v != nil {
		if receivedType(v) != "object" {
			r.Errors = append(r.Errors, validationError(file, "autoMode", "object", receivedType(v)))
		} else {
			var src protocol.JSONObject
			_ = src.UnmarshalJSON(v)
			next := protocol.NewJSONObject()
			for _, k := range []string{"allow", "soft_deny", "hard_deny", "environment", "classifyAllShell"} {
				v := src.Get(k)
				if v == nil {
					continue
				}
				expected := "array"
				if k == "classifyAllShell" {
					expected = "boolean"
				}
				if receivedType(v) != expected {
					r.Errors = append(r.Errors, validationError(file, "autoMode."+k, expected, receivedType(v)))
					continue
				}
				if expected == "array" {
					validateStringArray(v, r, file, "autoMode."+k)
				}
				next.Set(k, v)
			}
			raw, _ := next.MarshalJSON()
			out.Set("autoMode", raw)
		}
	}
	if v := input.Get("disableAutoMode"); v != nil {
		if string(v) != `"disable"` {
			r.Errors = append(r.Errors, enumError(file, "disableAutoMode", []string{"disable"}))
		} else {
			out.Set("disableAutoMode", v)
		}
	}
}
