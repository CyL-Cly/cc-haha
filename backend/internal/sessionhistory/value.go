// Package sessionhistory restores the original CLI transcript parent chain.
// It returns raw model-facing envelopes, never desktop display projections.
package sessionhistory

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

type StringComparator func(json.RawMessage, json.RawMessage) (int, error)

var ErrUnsupportedCollation = errors.New("session history collation requires well-formed Unicode strings")

// CompareStrings uses an independent English Unicode collator. Its declared
// profile is tested against Bun's host default locale; complete ICU/version
// and lone-surrogate collation remain a required integration boundary.
func CompareStrings(left, right json.RawMessage) (int, error) {
	values := [2]string{}
	for index, raw := range []json.RawMessage{left, right} {
		units, err := protocol.DecodeJavaScriptJSONString(raw)
		if err != nil {
			return 0, ErrUnsupportedCollation
		}
		for cursor := 0; cursor < len(units); cursor++ {
			unit := units[cursor]
			if unit >= 0xd800 && unit <= 0xdbff && cursor+1 < len(units) && units[cursor+1] >= 0xdc00 && units[cursor+1] <= 0xdfff {
				cursor++
			} else if unit >= 0xd800 && unit <= 0xdfff {
				return 0, ErrUnsupportedCollation
			}
		}
		values[index] = string(utf16.Decode(units))
	}
	return collate.New(language.English).CompareString(values[0], values[1]), nil
}

func quote(value string) json.RawMessage {
	return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(value)))
}
func encoded(value protocol.JSONObject) json.RawMessage { raw, _ := value.MarshalJSON(); return raw }
func object(raw json.RawMessage) protocol.JSONObject {
	var result protocol.JSONObject
	raw = bytes.TrimSpace(raw)
	if len(raw) != 0 && raw[0] == '{' {
		_ = json.Unmarshal(raw, &result)
	}
	return result
}
func isNull(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }
func truthy(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || isNull(raw) || bytes.Equal(raw, []byte("false")) {
		return false
	}
	if raw[0] == '"' {
		units, _ := protocol.DecodeJavaScriptJSONString(raw)
		return len(units) > 0
	}
	if raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9' {
		number, _ := strconv.ParseFloat(string(raw), 64)
		return number != 0
	}
	return true
}
func kind(value protocol.JSONObject) string { return value.String("type") }
func compact(value protocol.JSONObject) bool {
	return kind(value) == "system" && value.String("subtype") == "compact_boundary"
}
func array(raw json.RawMessage) ([]json.RawMessage, bool) {
	var values []json.RawMessage
	if raw = bytes.TrimSpace(raw); len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &values) != nil {
		return nil, false
	}
	return values, true
}

// JS spread/for-of iterates a string by codepoint, keeping unpaired UTF16 units.
func iterable(raw json.RawMessage) ([]json.RawMessage, bool) {
	if values, valid := array(raw); valid {
		return values, true
	}
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return nil, false
	}
	values := []json.RawMessage{}
	for index := 0; index < len(units); index++ {
		stop := index + 1
		if units[index] >= 0xd800 && units[index] <= 0xdbff && stop < len(units) && units[stop] >= 0xdc00 && units[stop] <= 0xdfff {
			stop++
		}
		values = append(values, protocol.QuoteJavaScriptUTF16(units[index:stop]))
		index = stop - 1
	}
	return values, true
}

func spread(raw json.RawMessage) protocol.JSONObject {
	if value := object(raw); value.IsObject() {
		return value
	}
	value := protocol.NewJSONObject()
	if items, valid := array(raw); valid {
		for index, item := range items {
			value.Set(strconv.Itoa(index), item)
		}
	} else if units, err := protocol.DecodeJavaScriptJSONString(raw); err == nil {
		for index, unit := range units {
			value.Set(strconv.Itoa(index), protocol.QuoteJavaScriptUTF16([]uint16{unit}))
		}
	}
	return value
}

// key preserves JS Map primitive identity, including missing versus null,
// rounded numbers, Infinity and lone UTF16 strings. Object-valued keys are
// local identities: JSON cannot make a parent's separately parsed object
// reference the UUID object of another record.
func key(raw json.RawMessage, identity int) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "undefined"
	}
	if raw[0] == '"' {
		units, _ := protocol.DecodeJavaScriptJSONString(raw)
		data := make([]byte, len(units)*2)
		for index, unit := range units {
			binary.LittleEndian.PutUint16(data[index*2:], unit)
		}
		return "string:" + string(data)
	}
	if raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9' {
		value, _ := strconv.ParseFloat(string(raw), 64)
		if value == 0 {
			value = 0
		}
		return "number:" + strconv.FormatUint(math.Float64bits(value), 16)
	}
	if raw[0] == '{' || raw[0] == '[' {
		return "object:" + strconv.Itoa(identity)
	}
	return string(raw)
}

type Pair struct{ Key, Value json.RawMessage }
type orderedMap struct {
	keys  []string
	pairs map[string]Pair
}

func newMap() orderedMap { return orderedMap{pairs: map[string]Pair{}} }
func (values *orderedMap) set(rawKey, value json.RawMessage) {
	name := key(rawKey, len(values.keys))
	if _, exists := values.pairs[name]; !exists {
		values.keys = append(values.keys, name)
	}
	values.pairs[name] = Pair{rawKey, value}
}
func (values orderedMap) entries() []Pair {
	result := []Pair{}
	for _, name := range values.keys {
		result = append(result, values.pairs[name])
	}
	return result
}
func (values orderedMap) MarshalJSON() ([]byte, error) {
	result := []json.RawMessage{}
	for _, pair := range values.entries() {
		left, right := pair.Key, pair.Value
		if left == nil {
			left = json.RawMessage("null")
		}
		if right == nil {
			right = json.RawMessage("null")
		}
		result = append(result, append(append(append([]byte{'['}, left...), ','), append(right, ']')...))
	}
	return json.Marshal(result)
}
