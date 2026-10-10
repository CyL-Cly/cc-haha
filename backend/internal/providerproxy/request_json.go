// Package providerproxy preserves the original protocol transforms shared by
// saved provider requests and the dedicated official OAuth model consumers.
package providerproxy

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func requestRecord(fields ...protocol.JSONField) protocol.JSONObject {
	return protocol.NewJSONObject(fields...)
}
func requestField(name string, value any) protocol.JSONField {
	return protocol.JSONField{Name: name, Value: requestEncode(value)}
}
func requestEncode(value any) json.RawMessage {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if encoder.Encode(value) != nil {
		return nil
	}
	return protocol.StringifyValidatedJavaScriptJSON(bytes.TrimSpace(out.Bytes()))
}
func requestObject(raw json.RawMessage) protocol.JSONObject {
	var value protocol.JSONObject
	_ = value.UnmarshalJSON(raw)
	return value
}
func requestArray(raw json.RawMessage) []json.RawMessage {
	var value []json.RawMessage
	_ = json.Unmarshal(raw, &value)
	return value
}
func requestIsString(raw json.RawMessage) bool { return len(raw) > 0 && raw[0] == '"' }
func requestIsArray(raw json.RawMessage) bool  { return len(raw) > 0 && raw[0] == '[' }
func requestTruthy(raw json.RawMessage) bool {
	if raw == nil || string(raw) == "null" || string(raw) == "false" {
		return false
	}
	if requestIsString(raw) {
		var value string
		_ = json.Unmarshal(raw, &value)
		return value != ""
	}
	if requestIsArray(raw) || len(raw) > 0 && raw[0] == '{' {
		return true
	}
	value, err := strconv.ParseFloat(string(raw), 64)
	return err != nil || value != 0 && !math.IsNaN(value)
}
func requestNumber(raw json.RawMessage) float64 {
	if raw == nil {
		return math.NaN()
	}
	if string(raw) == "null" || string(raw) == "false" {
		return 0
	}
	if string(raw) == "true" {
		return 1
	}
	if requestIsString(raw) {
		var value string
		_ = json.Unmarshal(raw, &value)
		value = protocol.TrimJavaScriptString(value)
		if value == "" {
			return 0
		}
		raw = []byte(value)
	}
	value, err := strconv.ParseFloat(string(raw), 64)
	if err != nil {
		return math.NaN()
	}
	return value
}
func requestJoin(values []json.RawMessage, separator string) json.RawMessage {
	value, _ := protocol.JoinJavaScriptValuesJSON(values, separator)
	return value
}
func requestString(value json.RawMessage) json.RawMessage {
	text, _ := protocol.JavaScriptStringJSON(value)
	return text
}
func requestFallback(value, fallback json.RawMessage) json.RawMessage {
	if value == nil || string(value) == "null" {
		return fallback
	}
	return value
}
