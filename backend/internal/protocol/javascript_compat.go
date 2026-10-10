package protocol

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
)

// deltaString preserves the raw Stream's JavaScript += behavior. Although
// Anthropic declares string deltas, the reference API does not validate that
// field: changing null or malformed proxy values into parse errors would alter
// existing assistant text and tool validation feedback.
func deltaString(object map[string]json.RawMessage, field string) string {
	return string(utf16.Decode(deltaStringUnits(object, field)))
}

func deltaStringUnits(object map[string]json.RawMessage, field string) []uint16 {
	raw, exists := object[field]
	if !exists {
		return utf16.Encode([]rune("undefined"))
	}
	return javascriptStringUnits(bytes.TrimSpace(raw))
}

// JavaScriptStringJSON exposes the existing String(value) coercion to session
// consumers without replacing lone UTF-16 units. Nil represents undefined;
// other inputs must be valid JSON, as they are in decoded provider events.
func JavaScriptStringJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`"undefined"`), nil
	}
	var value json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return json.RawMessage(quoteUTF16(javascriptStringUnits(bytes.TrimSpace(value)))), nil
}

// JoinJavaScriptValues matches Array.join for raw JSON values: null and an
// empty RawMessage (an omitted/undefined field) produce an empty item; other
// values use JavaScript String coercion. It does not select content blocks or
// alter their types, and returns an error for malformed JSON arguments.
func JoinJavaScriptValues(values []json.RawMessage, separator string) (string, error) {
	quoted, err := JoinJavaScriptValuesJSON(values, separator)
	if err != nil {
		return "", err
	}
	var text string
	_ = json.Unmarshal(quoted, &text)
	return text, nil
}

// JoinJavaScriptValuesJSON returns the joined UTF-16 string as quoted JSON,
// preserving lone surrogate code units for assistant history and the next
// model request. The unquoted Go string returned by JoinJavaScriptValues is
// suitable for UTF-8 terminal output, which replaces lone code units.
func JoinJavaScriptValuesJSON(values []json.RawMessage, separator string) (json.RawMessage, error) {
	for _, raw := range values {
		if len(raw) == 0 {
			continue
		}
		var source json.RawMessage
		if err := json.Unmarshal(raw, &source); err != nil {
			return nil, err
		}
	}
	return json.RawMessage(quoteUTF16(joinJavaScriptUnits(values, utf16.Encode([]rune(separator))))), nil
}

func joinJavaScriptUnits(values []json.RawMessage, separator []uint16) []uint16 {
	var joined []uint16
	for index, value := range values {
		if index > 0 {
			joined = append(joined, separator...)
		}
		raw := bytes.TrimSpace(value)
		if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
			continue
		}
		joined = append(joined, javascriptStringUnits(raw)...)
	}
	return joined
}

// Input is a validated JSON value. Keep strings as code units until the final
// quoted result, including nested array elements whose String coercion joins
// with commas and treats null as an empty element.
func javascriptStringUnits(raw []byte) []uint16 {
	switch raw[0] {
	case '"':
		return decodeJSString(raw)
	case '[':
		var values []json.RawMessage
		_ = json.Unmarshal(raw, &values)
		return joinJavaScriptUnits(values, []uint16{','})
	case '{':
		return utf16.Encode([]rune("[object Object]"))
	case 'n', 't', 'f':
		return utf16.Encode([]rune(string(raw)))
	default:
		number, _ := strconv.ParseFloat(string(raw), 64)
		return utf16.Encode([]rune(jsNumber(number)))
	}
}

func jsNumber(number float64) string {
	if math.IsInf(number, 0) {
		if number < 0 {
			return "-Infinity"
		}
		return "Infinity"
	}
	if number == 0 {
		return "0" // JavaScript String(-0) is "0".
	}
	if absolute := math.Abs(number); absolute >= 1e-6 && absolute < 1e21 {
		return strconv.FormatFloat(number, 'f', -1, 64)
	}
	// Both runtimes use shortest round-tripping decimal digits. JS removes
	// leading zeroes from exponent digits (e-07 -> e-7).
	value := strconv.FormatFloat(number, 'e', -1, 64)
	mantissa, exponent, _ := strings.Cut(value, "e")
	sign := exponent[:1]
	digits := strings.TrimLeft(exponent[1:], "0")
	return mantissa + "e" + sign + digits
}
