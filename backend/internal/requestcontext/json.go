package requestcontext

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func quoted(value string) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func rawNumber(number float64) json.RawMessage {
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return json.RawMessage("null")
	}
	return json.RawMessage(strconv.FormatFloat(number, 'g', -1, 64))
}
func rawObject(object protocol.JSONObject) json.RawMessage {
	raw, _ := object.MarshalJSON()
	return raw
}
func object(fields ...protocol.JSONField) protocol.JSONObject {
	return protocol.NewJSONObject(fields...)
}
func field(name string, raw json.RawMessage) protocol.JSONField {
	return protocol.JSONField{Name: name, Value: raw}
}
func parseObject(raw json.RawMessage) (protocol.JSONObject, error) {
	var value protocol.JSONObject
	if len(raw) == 0 {
		return value, nil
	}
	err := json.Unmarshal(raw, &value)
	return value, err
}
func stringValue(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}
func rawArray(values []json.RawMessage) json.RawMessage {
	if values == nil {
		values = []json.RawMessage{}
	}
	raw, _ := json.Marshal(values)
	return raw
}
func stringify(raw json.RawMessage) (json.RawMessage, error) {
	return protocol.ParseAndStringifyJavaScriptJSON(raw)
}
func truthy(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte("false")) {
		return false
	}
	switch raw[0] {
	case '{', '[', 't':
		return true
	case '"':
		return len(raw) > 2
	}
	number, _ := strconv.ParseFloat(string(raw), 64)
	return number != 0 && !math.IsNaN(number)
}
func space(unit uint16) bool {
	return unit >= 9 && unit <= 13 || unit == 32 || unit == 0xa0 || unit == 0x1680 || unit >= 0x2000 && unit <= 0x200a || unit == 0x2028 || unit == 0x2029 || unit == 0x202f || unit == 0x205f || unit == 0x3000 || unit == 0xfeff
}
func trim(value string) string {
	units, _ := protocol.DecodeJavaScriptJSONString(quoted(value))
	for len(units) > 0 && space(units[0]) {
		units = units[1:]
	}
	for len(units) > 0 && space(units[len(units)-1]) {
		units = units[:len(units)-1]
	}
	return stringValue(protocol.QuoteJavaScriptUTF16(units))
}
func envTrue(value string) bool {
	switch strings.ToLower(trim(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
func envFalse(value string) bool {
	switch strings.ToLower(trim(value)) {
	case "0", "false", "no", "off":
		return true
	}
	return false
}
func toStorage(value protocol.JSONObject) storage.Object {
	var result storage.Object
	_ = json.Unmarshal(rawObject(value), &result)
	return result
}
func fromStorage(value storage.Object) protocol.JSONObject {
	raw, _ := json.Marshal(value)
	result, _ := parseObject(raw)
	return result
}
func replaceContent(envelope protocol.JSONObject, content json.RawMessage) protocol.JSONObject {
	api, _ := parseObject(envelope.Get("message"))
	api.Set("content", content)
	copy := envelope.Clone()
	copy.Set("message", rawObject(api))
	return copy
}
func content(envelope protocol.JSONObject) json.RawMessage {
	api, _ := parseObject(envelope.Get("message"))
	return api.Get("content")
}
func blocks(raw json.RawMessage) []protocol.JSONObject {
	var result []protocol.JSONObject
	_ = json.Unmarshal(raw, &result)
	return result
}
func isThinking(block protocol.JSONObject) bool {
	kind := block.String("type")
	return kind == "thinking" || kind == "redacted_thinking"
}
