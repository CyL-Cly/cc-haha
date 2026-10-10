package streamhistory

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var decimalNumberString = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

// positive implements only the parsed-JSON > 0 comparison used by updateUsage.
// It retains the original value on success; conversion is not a normalization
// of provider counters. Reuse the shared UTF-16/String codec for array joins.
func positive(raw json.RawMessage) (bool, error) {
	raw = bytes.TrimSpace(raw)
	if nullish(raw) || bytes.Equal(raw, []byte("false")) {
		return false, nil
	}
	if bytes.Equal(raw, []byte("true")) {
		return true, nil
	}
	if raw[0] != '"' && raw[0] != '[' && raw[0] != '{' {
		number, _ := strconv.ParseFloat(string(raw), 64)
		return number > 0, nil
	}
	if err := validateJSONDefaultPrimitive(raw); err != nil {
		return false, err
	}
	if raw[0] == '{' {
		return false, nil // Object.prototype.toString gives [object Object].
	}
	quoted, err := protocol.JavaScriptStringJSON(raw)
	if err != nil {
		return false, err
	}
	units, err := protocol.DecodeJavaScriptJSONString(quoted)
	if err != nil {
		return false, err
	}
	for len(units) > 0 && jsSpace(units[0]) {
		units = units[1:]
	}
	for len(units) > 0 && jsSpace(units[len(units)-1]) {
		units = units[:len(units)-1]
	}
	text := make([]byte, len(units))
	for index, unit := range units {
		if unit > 127 {
			return false, nil // Non-ASCII numeric text, including lone UTF-16.
		}
		text[index] = byte(unit)
	}
	value := string(text)
	if value == "Infinity" || value == "+Infinity" {
		return true, nil
	}
	if len(value) >= 2 && value[0] == '0' {
		base := 0
		switch value[1] {
		case 'x', 'X':
			base = 16
		case 'b', 'B':
			base = 2
		case 'o', 'O':
			base = 8
		}
		if base != 0 {
			return positiveRadixDigits(value[2:], base), nil
		}
	}
	if !decimalNumberString.MatchString(value) {
		return false, nil
	}
	number, _ := strconv.ParseFloat(value, 64)
	return number > 0, nil
}

// JSON.parse creates ordinary objects/arrays, with no functions or symbols.
// An own non-callable toString shadows the default method; neither the native
// valueOf nor any JSON valueOf can then return a primitive. Bun's real Source
// feedback for this TypeError is "No default value", established by the oracle.
func validateJSONDefaultPrimitive(raw json.RawMessage) error {
	raw = bytes.TrimSpace(raw)
	if nullish(raw) {
		return nil
	}
	switch raw[0] {
	case '{':
		if object(raw).Get("toString") != nil {
			return sourceError("No default value")
		}
	case '[':
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		for _, value := range values {
			if err := validateJSONDefaultPrimitive(value); err != nil {
				return err
			}
		}
	}
	return nil
}

func positiveRadixDigits(value string, base int) bool {
	if value == "" {
		return false
	}
	positive := false
	for _, char := range []byte(value) {
		digit := byte(255)
		switch {
		case char >= '0' && char <= '9':
			digit = char - '0'
		case char >= 'a' && char <= 'f':
			digit = char - 'a' + 10
		case char >= 'A' && char <= 'F':
			digit = char - 'A' + 10
		}
		if int(digit) >= base {
			return false
		}
		positive = positive || digit != 0
	}
	return positive
}
