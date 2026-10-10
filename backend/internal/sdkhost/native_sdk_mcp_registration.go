package sdkhost

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// sdkMCPInitializeNames follows the original truthy + length > 0 gate and
// for-of. These values arrive through the raw StructuredIO input, not a Zod
// string-array parser. Each configuration retains the original name value.
func sdkMCPInitializeNames(value json.RawMessage) ([]json.RawMessage, error) {
	if !openaiauth.AuthTruthy(value) {
		return nil, nil
	}
	value = bytes.TrimSpace(value)
	switch value[0] {
	case '[':
		var names []json.RawMessage
		err := json.Unmarshal(value, &names)
		return names, err
	case '"':
		units, err := protocol.DecodeJavaScriptJSONString(value)
		if err != nil {
			return nil, err
		}
		var names []json.RawMessage
		for i := 0; i < len(units); {
			end := i + 1
			if units[i] >= 0xd800 && units[i] <= 0xdbff && end < len(units) && units[end] >= 0xdc00 && units[end] <= 0xdfff {
				end++
			}
			names = append(names, protocol.QuoteJavaScriptUTF16(units[i:end]))
			i = end
		}
		return names, nil
	case '{':
		obj := object(value)
		length := obj.Get("length")
		if length == nil {
			return nil, nil
		}
		coerced, err := protocol.ExactJavaScriptStringJSON(length)
		if err != nil {
			return nil, err
		}
		var text string
		_ = json.Unmarshal(coerced, &text)
		number, _ := strconv.ParseFloat(protocol.TrimJavaScriptString(text), 64)
		if string(length) == "true" {
			number = 1
		}
		if number > 0 {
			return nil, errors.New("{} is not iterable")
		}
	}
	return nil, nil
}

func (s *NativeSDKMCPState) RegisterInitializeConfigurations(scope *cooperative.Scope, value json.RawMessage) error {
	if s == nil || !scope.BelongsTo(s.owner) {
		return cooperative.ErrRestricted
	}
	names, err := sdkMCPInitializeNames(value)
	if err != nil {
		return err
	}
	for _, name := range names {
		key, err := protocol.ExactJavaScriptStringJSON(name)
		if err != nil {
			return err
		}
		// Ordinary-object __proto__ assignment affects its prototype, so it
		// contributes no enumerable configuration to Object.entries.
		if bytes.Equal(key, []byte(`"__proto__"`)) {
			continue
		}
		config := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("sdk")}, protocol.JSONField{Name: "name", Value: name})
		if err = s.configurations.SetPropertyJSON(key, raw(config)); err != nil {
			return err
		}
	}
	return nil
}
