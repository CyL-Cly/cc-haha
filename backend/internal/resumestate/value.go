// Package resumestate implements the CLI's loadConversationForResume state
// orchestration. Its output remains model-facing history, not desktop messages.
package resumestate

import (
	"bytes"
	"encoding/json"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionresume"
)

type CapabilityError struct{ Capability string }

func (err *CapabilityError) Error() string {
	return "resume capability is not implemented: " + err.Capability
}
func object(raw json.RawMessage) protocol.JSONObject {
	var value protocol.JSONObject
	_ = json.Unmarshal(raw, &value)
	return value
}
func encode(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func quote(value string) json.RawMessage {
	return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(value)))
}
func units(raw json.RawMessage) ([]uint16, bool) {
	value, err := protocol.DecodeJavaScriptJSONString(raw)
	return value, err == nil
}
func text(raw json.RawMessage) string { value, _ := units(raw); return string(utf16.Decode(value)) }
func isNull(raw json.RawMessage) bool { return bytes.Equal(raw, []byte("null")) }
func truthy(raw json.RawMessage) bool {
	if len(raw) == 0 || isNull(raw) || bytes.Equal(raw, []byte("false")) {
		return false
	}
	if value, ok := units(raw); ok {
		return len(value) > 0
	}
	if raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9' {
		number, _ := strconv.ParseFloat(string(raw), 64)
		return number != 0 && !math.IsNaN(number)
	}
	return true
}
func array(raw json.RawMessage) ([]json.RawMessage, bool) {
	var items []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &items) != nil {
		return nil, false
	}
	return items, true
}
func jsString(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return quote("undefined"), nil
	}
	return protocol.JavaScriptStringJSON(raw)
}
func scalarEqual(left, right json.RawMessage) bool {
	if len(left) == 0 || len(right) == 0 {
		return len(left) == len(right)
	}
	if left[0] == '{' || left[0] == '[' || right[0] == '{' || right[0] == '[' {
		return false
	}
	if l, ok := units(left); ok {
		r, ok := units(right)
		if !ok || len(l) != len(r) {
			return false
		}
		for i := range l {
			if l[i] != r[i] {
				return false
			}
		}
		return true
	}
	if left[0] == '-' || left[0] >= '0' && left[0] <= '9' {
		if right[0] != '-' && (right[0] < '0' || right[0] > '9') {
			return false
		}
		a, _ := strconv.ParseFloat(string(left), 64)
		b, _ := strconv.ParseFloat(string(right), 64)
		return a == b
	}
	return bytes.Equal(left, right)
}
func property(raw json.RawMessage, name, expression string) (json.RawMessage, error) {
	if len(raw) == 0 || isNull(raw) {
		label := "undefined"
		if isNull(raw) {
			label = "null"
		}
		return nil, &sessionresume.Failure{Name: "TypeError", Message: label + " is not an object (evaluating '" + expression + "')"}
	}
	return object(raw).Get(name), nil
}
func jsonNumber(raw json.RawMessage) float64 {
	value, err := strconv.ParseFloat(string(raw), 64)
	if err != nil && !math.IsInf(value, 0) {
		return math.NaN()
	}
	return value
}

var decimalNumber = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

func jsWhitespace(unit uint16) bool {
	return unit >= '\t' && unit <= '\r' || unit == ' ' || unit == 0xa0 || unit == 0x1680 || unit >= 0x2000 && unit <= 0x200a || unit == 0x2028 || unit == 0x2029 || unit == 0x202f || unit == 0x205f || unit == 0x3000 || unit == 0xfeff
}
func validateStringCoercion(raw json.RawMessage) error {
	if value := object(raw); value.IsObject() && value.Get("toString") != nil {
		return &sessionresume.Failure{Name: "TypeError", Message: "No default value"}
	}
	if values, ok := array(raw); ok {
		for _, value := range values {
			if err := validateStringCoercion(value); err != nil {
				return err
			}
		}
	}
	return nil
}
func coerceNumber(raw json.RawMessage) (float64, error) {
	if isNull(raw) || string(raw) == "false" {
		return 0, nil
	}
	if string(raw) == "true" {
		return 1, nil
	}
	if value, ok := units(raw); ok {
		for len(value) > 0 && jsWhitespace(value[0]) {
			value = value[1:]
		}
		for len(value) > 0 && jsWhitespace(value[len(value)-1]) {
			value = value[:len(value)-1]
		}
		raw = []byte(string(utf16.Decode(value)))
		if len(raw) == 0 {
			return 0, nil
		}
		source := string(raw)
		if source == "Infinity" || source == "+Infinity" {
			return math.Inf(1), nil
		}
		if source == "-Infinity" {
			return math.Inf(-1), nil
		}
		if len(source) >= 2 && source[0] == '0' {
			base := 0
			switch source[1] {
			case 'x', 'X':
				base = 16
			case 'o', 'O':
				base = 8
			case 'b', 'B':
				base = 2
			}
			if base != 0 {
				integer, valid := new(big.Int).SetString(source[2:], base)
				if !valid || len(source[2:]) == 0 || source[2] == '+' || source[2] == '-' {
					return math.NaN(), nil
				}
				number, _ := new(big.Float).SetInt(integer).Float64()
				return number, nil
			}
		}
		if !decimalNumber.MatchString(source) {
			return math.NaN(), nil
		}
		return jsonNumber(raw), nil
	}
	if values, ok := array(raw); ok {
		if err := validateStringCoercion(raw); err != nil {
			return 0, err
		}
		converted, err := jsString(encode(values))
		if err != nil {
			return 0, err
		}
		return coerceNumber(converted)
	}
	if err := validateStringCoercion(raw); err != nil {
		return 0, err
	}
	return jsonNumber(raw), nil
}
func numeric(value float64) json.RawMessage {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return json.RawMessage("null")
	}
	return []byte(strconv.FormatFloat(value, 'f', -1, 64))
}
