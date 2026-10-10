package migrations

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Keep raw numeric values until application decisions are made: JSON.parse
// accepts 1e400 as Infinity, while JSON.stringify later writes it as null.
// Quoted keys retain UTF-16 identity and first insertion position, including
// duplicate names. A Go map would change whether the TS upgrader rewrites.
type entry struct {
	key   string
	value json.RawMessage
}
type object []entry

func parseObject(raw json.RawMessage) (object, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' || !json.Valid(raw) {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if _, err := decoder.Token(); err != nil {
		return nil, false
	}
	result := object{}
	for decoder.More() {
		start := decoder.InputOffset()
		if _, err := decoder.Token(); err != nil {
			return nil, false
		}
		keyRaw := bytes.TrimSpace(raw[start:decoder.InputOffset()])
		keyRaw = bytes.TrimSpace(bytes.TrimPrefix(keyRaw, []byte(",")))
		key, err := protocol.ParseAndStringifyJavaScriptJSON(keyRaw)
		if err != nil {
			return nil, false
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, false
		}
		result.setKey(string(key), value)
	}
	return result, true
}

func (value object) get(name string) json.RawMessage {
	key := quote(name)
	for _, item := range value {
		if item.key == string(key) {
			return item.value
		}
	}
	return nil
}
func (value *object) set(name string, raw json.RawMessage) { value.setKey(string(quote(name)), raw) }
func (value *object) setKey(key string, raw json.RawMessage) {
	for index := range *value {
		if (*value)[index].key == key {
			(*value)[index].value = raw
			return
		}
	}
	*value = append(*value, entry{key, raw})
}
func (value *object) remove(name string) {
	key := string(quote(name))
	for index := range *value {
		if (*value)[index].key == key {
			*value = append((*value)[:index], (*value)[index+1:]...)
			return
		}
	}
}
func (value object) clone() object { return append(object{}, value...) }
func (value object) raw() json.RawMessage {
	var output bytes.Buffer
	output.WriteByte('{')
	for index, item := range value {
		if index > 0 {
			output.WriteByte(',')
		}
		output.WriteString(item.key)
		output.WriteByte(':')
		output.Write(item.value)
	}
	output.WriteByte('}')
	return output.Bytes()
}
func parseArray(raw json.RawMessage) ([]json.RawMessage, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '[' {
		return nil, false
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, false
	}
	return values, true
}
func arrayRaw(values []json.RawMessage) json.RawMessage {
	if len(values) == 0 {
		return json.RawMessage("[]")
	}
	return json.RawMessage("[" + strings.Join(rawStrings(values), ",") + "]")
}
func rawStrings(values []json.RawMessage) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}
func quote(value string) json.RawMessage {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	return bytes.TrimSpace(output.Bytes())
}
func stringValue(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '"'
}
func stringKey(raw json.RawMessage) string {
	if !stringValue(raw) {
		return ""
	}
	value, _ := protocol.ParseAndStringifyJavaScriptJSON(raw)
	return string(value)
}
func equalStrings(left, right json.RawMessage) bool {
	return stringValue(left) && stringValue(right) && stringKey(left) == stringKey(right)
}
func isNull(raw json.RawMessage) bool { return len(raw) == 0 || string(bytes.TrimSpace(raw)) == "null" }
func truthy(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte("false")) {
		return false
	}
	if raw[0] == '"' {
		return stringKey(raw) != `""`
	}
	if raw[0] == '{' || raw[0] == '[' || bytes.Equal(raw, []byte("true")) {
		return true
	}
	number, _ := strconv.ParseFloat(string(raw), 64)
	return number != 0 && !math.IsNaN(number)
}
func numberOr(raw json.RawMessage, fallback float64) float64 {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || !(raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9') {
		return fallback
	}
	value, _ := strconv.ParseFloat(string(raw), 64)
	return value
}
func stableJSON(raw json.RawMessage) ([]byte, error) {
	canonical, err := protocol.ParseAndStringifyJavaScriptJSON(raw)
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := json.Indent(&output, canonical, "", "  "); err != nil {
		return nil, err
	}
	output.WriteByte('\n')
	return output.Bytes(), nil
}
