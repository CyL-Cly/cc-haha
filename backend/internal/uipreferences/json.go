package uipreferences

import (
	"bytes"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Preferences retains application values until JSON serialization. In
// particular, schema decisions see JSON.parse's overflowing number rather
// than an already stringified null. Its wire boundary uses JS stringify.
type Preferences json.RawMessage

func (value Preferences) MarshalJSON() ([]byte, error) {
	return protocol.ParseAndStringifyJavaScriptJSON(value)
}
func (value *Preferences) UnmarshalJSON(raw []byte) error {
	var parsed json.RawMessage
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return err
	}
	*value = Preferences(parsed)
	return nil
}

type property struct {
	key   string
	value json.RawMessage
}
type object []property

func parseObject(raw json.RawMessage) (object, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' || !json.Valid(raw) {
		return object{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	_, _ = decoder.Token()
	value := object{}
	positions := map[string]int{}
	for decoder.More() {
		start := decoder.InputOffset()
		_, _ = decoder.Token()
		keyRaw := bytes.TrimSpace(raw[start:decoder.InputOffset()])
		keyRaw = bytes.TrimSpace(bytes.TrimPrefix(keyRaw, []byte(",")))
		units, _ := protocol.DecodeJavaScriptJSONString(keyRaw)
		var item json.RawMessage
		_ = decoder.Decode(&item)
		key := string(protocol.QuoteJavaScriptUTF16(units))
		if index, exists := positions[key]; exists {
			value[index].value = item
		} else {
			positions[key] = len(value)
			value = append(value, property{key, item})
		}
	}
	// Object.entries enumerates array-index keys first, before the 2,000-entry
	// cap. Other keys keep their first insertion position after duplicates.
	sort.SliceStable(value, func(left, right int) bool {
		a, ai := arrayIndex(value[left].key)
		b, bi := arrayIndex(value[right].key)
		if ai && bi {
			return a < b
		}
		return ai && !bi
	})
	return value, true
}
func arrayIndex(key string) (uint64, bool) {
	name := key[1 : len(key)-1]
	index, err := strconv.ParseUint(name, 10, 32)
	return index, err == nil && index < math.MaxUint32 && strconv.FormatUint(index, 10) == name
}
func (value object) get(name string) json.RawMessage {
	key := string(quoted(name))
	for _, item := range value {
		if item.key == key {
			return item.value
		}
	}
	return nil
}
func (value *object) set(name string, raw json.RawMessage) {
	value.setQuoted(string(quoted(name)), raw)
}
func (value *object) setQuoted(key string, raw json.RawMessage) {
	for index := range *value {
		if (*value)[index].key == key {
			(*value)[index].value = raw
			return
		}
	}
	*value = append(*value, property{key, raw})
}
func (value *object) removeQuoted(key string) {
	for index := range *value {
		if (*value)[index].key == key {
			*value = append((*value)[:index], (*value)[index+1:]...)
			return
		}
	}
}
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
func merge(current, patch object) object {
	result := append(object{}, current...)
	for _, item := range patch {
		result.setQuoted(item.key, item.value)
	}
	return result
}
func quoted(value string) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func units(raw json.RawMessage) ([]uint16, bool) {
	value, err := protocol.DecodeJavaScriptJSONString(raw)
	return value, err == nil
}
func stringIs(raw json.RawMessage, candidate string) bool {
	value, valid := units(raw)
	return valid && string(protocol.QuoteJavaScriptUTF16(value)) == string(quoted(candidate))
}
func quotedUnits(value []uint16) json.RawMessage { return protocol.QuoteJavaScriptUTF16(value) }
func jsWhitespace(value uint16) bool {
	return value >= '\t' && value <= '\r' || value == ' ' || value == 0x00a0 || value == 0x1680 || value >= 0x2000 && value <= 0x200a || value == 0x2028 || value == 0x2029 || value == 0x202f || value == 0x205f || value == 0x3000 || value == 0xfeff
}
func trim(value []uint16) []uint16 {
	start, end := 0, len(value)
	for start < end && jsWhitespace(value[start]) {
		start++
	}
	for start < end && jsWhitespace(value[end-1]) {
		end--
	}
	return value[start:end]
}
func collapse(value []uint16) []uint16 {
	value = trim(value)
	result := []uint16{}
	space := false
	for _, unit := range value {
		if jsWhitespace(unit) {
			if !space {
				result = append(result, ' ')
			}
			space = true
		} else {
			result = append(result, unit)
			space = false
		}
	}
	return result
}
func number(raw json.RawMessage) (float64, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || !(raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9') {
		return 0, false
	}
	value, _ := strconv.ParseFloat(string(raw), 64)
	return value, true
}
func boolOr(raw json.RawMessage, fallback bool) json.RawMessage {
	switch strings.TrimSpace(string(raw)) {
	case "true":
		return json.RawMessage("true")
	case "false":
		return json.RawMessage("false")
	}
	if fallback {
		return json.RawMessage("true")
	}
	return json.RawMessage("false")
}
func normalizedChoice(raw json.RawMessage, choices []string, fallback string) json.RawMessage {
	for _, choice := range choices {
		if stringIs(raw, choice) {
			return raw
		}
	}
	return quoted(fallback)
}
func normalizedStringArray(raw json.RawMessage) json.RawMessage {
	var source []json.RawMessage
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '[' || json.Unmarshal(raw, &source) != nil {
		return json.RawMessage("[]")
	}
	values := []string{}
	seen := map[string]bool{}
	for _, item := range source {
		value, valid := units(item)
		if !valid || len(value) == 0 {
			continue
		}
		key := string(quotedUnits(value))
		if seen[key] {
			continue
		}
		seen[key] = true
		values = append(values, key)
		if len(values) >= 2000 {
			break
		}
	}
	return json.RawMessage("[" + strings.Join(values, ",") + "]")
}
