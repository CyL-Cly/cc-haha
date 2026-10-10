package activitystatsapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type undefinedValue struct{}

var undefined = undefinedValue{}

type object = map[string]any

func decode(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if !json.Valid(raw) {
		return nil, errors.New("Invalid JSON")
	}
	return numbers(value), nil
}
func numbers(value any) any {
	switch v := value.(type) {
	case json.Number:
		n, _ := strconv.ParseFloat(string(v), 64)
		return n
	case []any:
		if len(v) == 0 {
			return make([]any, 0, 1)
		}
		for i, entry := range v {
			v[i] = numbers(entry)
		}
	case map[string]any:
		for name, entry := range v {
			v[name] = numbers(entry)
		}
	}
	return value
}
func get(value any, key string) any {
	if v, ok := value.(map[string]any); ok {
		if entry, exists := v[key]; exists {
			return entry
		}
	}
	if v, ok := value.([]any); ok {
		if index, err := strconv.Atoi(key); err == nil && index >= 0 && index < len(v) {
			return v[index]
		}
	}
	return undefined
}
func nullish(value any) bool {
	if value == nil {
		return true
	}
	_, ok := value.(undefinedValue)
	return ok
}
func strictEqual(a, b any) bool {
	switch left := a.(type) {
	case nil:
		return b == nil
	case undefinedValue:
		_, ok := b.(undefinedValue)
		return ok
	case float64:
		right, ok := b.(float64)
		return ok && left == right
	case string:
		right, ok := b.(string)
		return ok && left == right
	case bool:
		right, ok := b.(bool)
		return ok && left == right
	}
	return false
}
func mapKey(value any) string {
	switch value.(type) {
	case map[string]any, []any:
		return fmt.Sprintf("%T:%p", value, value)
	}
	return fmt.Sprintf("%T:%s", value, stringValue(value))
}
func truthy(value any) bool {
	switch v := value.(type) {
	case nil, undefinedValue:
		return false
	case bool:
		return v
	case float64:
		return v != 0 && !math.IsNaN(v)
	case string:
		return v != ""
	}
	return true
}
func orZero(value any) any {
	if !truthy(value) {
		return float64(0)
	}
	return value
}
func stringValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case nil:
		return "null"
	case undefinedValue:
		return "undefined"
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		if math.IsNaN(v) {
			return "NaN"
		}
		if math.IsInf(v, 1) {
			return "Infinity"
		}
		if math.IsInf(v, -1) {
			return "-Infinity"
		}
		raw, _ := json.Marshal(v)
		quoted, _ := protocol.JavaScriptStringJSON(raw)
		var text string
		_ = json.Unmarshal(quoted, &text)
		return text
	case []any:
		parts := []string{}
		for _, entry := range v {
			if nullish(entry) {
				parts = append(parts, "")
			} else {
				parts = append(parts, stringValue(entry))
			}
		}
		return strings.Join(parts, ",")
	default:
		return "[object Object]"
	}
}
func numberValue(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case nil:
		return 0
	case undefinedValue:
		return math.NaN()
	case bool:
		if v {
			return 1
		}
		return 0
	case string:
		trimmed := trim(v)
		if trimmed == "" {
			return 0
		}
		if strings.HasPrefix(trimmed, "0x") || strings.HasPrefix(trimmed, "0X") {
			n, err := strconv.ParseUint(trimmed[2:], 16, 64)
			if err == nil {
				return float64(n)
			}
		}
		n, err := strconv.ParseFloat(trimmed, 64)
		if err == nil || math.IsInf(n, 0) {
			return n
		}
		return math.NaN()
	default:
		return numberValue(stringValue(v))
	}
}
func primitive(value any) any {
	switch value.(type) {
	case map[string]any, []any:
		return stringValue(value)
	}
	return value
}
func add(left, right any) any {
	left, right = primitive(left), primitive(right)
	if _, ok := left.(string); ok {
		return stringValue(left) + stringValue(right)
	}
	if _, ok := right.(string); ok {
		return stringValue(left) + stringValue(right)
	}
	return numberValue(left) + numberValue(right)
}
func fields(value any) (object, error) {
	if nullish(value) {
		return nil, errors.New("Cannot convert undefined or null to object")
	}
	return spread(value), nil
}
func spread(value any) object {
	out := object{}
	switch v := value.(type) {
	case map[string]any:
		for key, entry := range v {
			out[key] = entry
		}
	case []any:
		for i, entry := range v {
			out[strconv.Itoa(i)] = entry
		}
	case string:
		for i, unit := range utf16.Encode([]rune(v)) {
			out[strconv.Itoa(i)] = string(utf16.Decode([]uint16{unit}))
		}
	}
	return out
}
func clean(value any) any {
	switch v := value.(type) {
	case undefinedValue:
		return nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil
		}
		return v
	case map[string]any:
		out := object{}
		for key, entry := range v {
			if _, missing := entry.(undefinedValue); !missing {
				out[key] = clean(entry)
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, entry := range v {
			out[i] = clean(entry)
		}
		return out
	}
	return value
}
func encode(value any) []byte { raw, _ := json.Marshal(clean(value)); return raw }
func trim(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		return r >= '\t' && r <= '\r' || r == ' ' || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
	})
}
func sortedKeys(value object) []string {
	keys := []string{}
	for key := range value {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return utf16Less(keys[i], keys[j]) })
	return keys
}
func utf16Less(left, right string) bool {
	a, b := utf16.Encode([]rune(left)), utf16.Encode([]rune(right))
	for i := 0; i < min(len(a), len(b)); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}
func ownKeys(value object) []string {
	keys := sortedKeys(value)
	sort.SliceStable(keys, func(i, j int) bool {
		a, ae := strconv.ParseUint(keys[i], 10, 32)
		b, be := strconv.ParseUint(keys[j], 10, 32)
		ai := ae == nil && strconv.FormatUint(a, 10) == keys[i] && a < 4294967295
		bi := be == nil && strconv.FormatUint(b, 10) == keys[j] && b < 4294967295
		if ai && bi {
			return a < b
		}
		return ai && !bi
	})
	return keys
}
