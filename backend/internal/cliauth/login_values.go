package cliauth

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dop251/goja"
)

// The original client applies ECMAScript multiplication/Date conversion to
// issuer fields; JSON decoding alone loses string/null/missing distinctions.
func oauthLifetime(now float64, raw json.RawMessage) json.RawMessage {
	vm := goja.New()
	_ = vm.Set("now", now)
	expression := "undefined"
	if raw != nil {
		quoted, _ := json.Marshal(string(raw))
		expression = "JSON.parse(" + string(quoted) + ")"
	}
	value, err := vm.RunString("JSON.stringify(now + (" + expression + ") * 1000)")
	if err != nil {
		return json.RawMessage("null")
	}
	return json.RawMessage(value.String())
}
func ValidFirstTokenDate(raw json.RawMessage) bool {
	if raw == nil || string(raw) == "null" {
		return true
	}
	quoted, _ := json.Marshal(string(raw))
	value, err := goja.New().RunString("!isNaN(new Date(JSON.parse(" + string(quoted) + ")).getTime())")
	return err == nil && value.ToBoolean()
}

func OAuthTokenExpired(raw json.RawMessage, now float64) bool {
	if string(raw) == "null" {
		return false
	}
	vm := goja.New()
	_ = vm.Set("now", now)
	expression := "undefined"
	if raw != nil {
		quoted, _ := json.Marshal(string(raw))
		expression = "JSON.parse(" + string(quoted) + ")"
	}
	value, err := vm.RunString("now + 300000 >= (" + expression + ")")
	return err == nil && value.ToBoolean()
}

func OAuthFieldString(raw json.RawMessage) string {
	encoded, err := protocol.ExactJavaScriptStringJSON(raw)
	if err != nil {
		return ""
	}
	var value string
	_ = json.Unmarshal(encoded, &value)
	return value
}
