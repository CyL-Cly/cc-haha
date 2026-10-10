package protocol

import (
	"bytes"
	"encoding/json"
	"strconv"
	"unicode/utf16"
)

// ExactJSONStringTypeError is the pinned JavaScript ordinary-conversion failure.
// Consumers project it at their own schema, attachment or host error boundary.
type ExactJSONStringTypeError struct{ Message string }

func (failure *ExactJSONStringTypeError) Error() string { return failure.Message }

func ExactJavaScriptStringJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`"undefined"`), nil
	}
	var validated json.RawMessage
	if err := json.Unmarshal(raw, &validated); err != nil {
		return nil, err
	}
	units, err := exactJSONStringUnits(bytes.TrimSpace(validated))
	if err != nil {
		return nil, err
	}
	return QuoteJavaScriptUTF16(units), nil
}

func exactJSONStringUnits(raw json.RawMessage) ([]uint16, error) {
	switch raw[0] {
	case '"':
		return DecodeJavaScriptJSONString(raw)
	case '[':
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, err
		}
		units := make([]uint16, 0)
		for index, value := range values {
			if index > 0 {
				units = append(units, ',')
			}
			value = bytes.TrimSpace(value)
			if bytes.Equal(value, []byte("null")) {
				continue
			}
			item, err := exactJSONStringUnits(value)
			if err != nil {
				return nil, err
			}
			units = append(units, item...)
		}
		return units, nil
	case '{':
		var object JSONObject
		if err := json.Unmarshal(raw, &object); err != nil {
			return nil, err
		}
		// JSON has no callable values. An own toString shadows the inherited
		// function; the default valueOf returns the object, so coercion fails.
		if object.Get("toString") != nil {
			return nil, &ExactJSONStringTypeError{Message: "No default value"}
		}
		return utf16.Encode([]rune("[object Object]")), nil
	case 'n', 't', 'f':
		return utf16.Encode([]rune(string(raw))), nil
	default:
		number, _ := strconv.ParseFloat(string(raw), 64)
		return utf16.Encode([]rune(jsNumber(number))), nil
	}
}
