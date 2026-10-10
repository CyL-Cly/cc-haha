// Package textsanitization preserves the shipped Unicode sanitization used
// when tools/prompts from an MCP peer enter the Harness.
package textsanitization

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func normalize(units []uint16) ([]uint16, error) {
	table, err := tables()
	if err != nil {
		return nil, err
	}
	out := []uint16{}
	segment := []uint16{}
	flush := func() {
		text := table.nfkc(utf16.Decode(segment))
		segment = nil
		for _, r := range text {
			if table.allowed[r] {
				out = append(out, utf16.Encode([]rune{r})...)
			}
		}
	}
	for i := 0; i < len(units); i++ {
		c := units[i]
		if c >= 0xd800 && c <= 0xdbff && i+1 < len(units) && units[i+1] >= 0xdc00 && units[i+1] <= 0xdfff {
			segment = append(segment, c, units[i+1])
			i++
			continue
		}
		if c >= 0xd800 && c <= 0xdfff {
			flush()
			out = append(out, c)
			continue
		}
		segment = append(segment, c)
	}
	flush()
	return out, nil
}
func PartiallySanitize(value json.RawMessage) (json.RawMessage, error) {
	current, err := protocol.DecodeJavaScriptJSONString(value)
	if err != nil {
		return nil, err
	}
	original := append([]uint16(nil), current...)
	previous := []uint16{}
	iterations := 0
	for !slices.Equal(current, previous) && iterations < 10 {
		previous = append([]uint16(nil), current...)
		current, err = normalize(current)
		if err != nil {
			return nil, err
		}
		iterations++
	}
	if iterations >= 10 {
		if len(original) > 100 {
			original = original[:100]
		}
		return nil, fmt.Errorf("Unicode sanitization reached maximum iterations (10) for input: %s", string(utf16.Decode(original)))
	}
	return protocol.QuoteJavaScriptUTF16(current), nil
}
func Recursive(value json.RawMessage) (json.RawMessage, error) {
	value = bytes.TrimSpace(value)
	if !json.Valid(value) {
		return nil, errors.New("Unicode sanitization requires valid JSON")
	}
	switch value[0] {
	case '"':
		return PartiallySanitize(value)
	case '[':
		var values []json.RawMessage
		_ = json.Unmarshal(value, &values)
		for i, v := range values {
			next, err := Recursive(v)
			if err != nil {
				return nil, err
			}
			values[i] = next
		}
		data, err := json.Marshal(values)
		return protocol.StringifyValidatedJavaScriptJSON(data), err
	case '{':
		var input protocol.JSONObject
		_ = input.UnmarshalJSON(value)
		out := protocol.NewJSONObject()
		for _, p := range input.OwnProperties() {
			key, err := PartiallySanitize(p.NameJSON)
			if err != nil {
				return nil, err
			}
			var name string
			_ = json.Unmarshal(key, &name)
			if name == "__proto__" {
				continue
			}
			next, err := Recursive(p.Value)
			if err != nil {
				return nil, err
			}
			if err := out.SetPropertyJSON(key, next); err != nil {
				return nil, err
			}
		}
		return out.MarshalJSON()
	default:
		return protocol.StringifyValidatedJavaScriptJSON(value), nil
	}
}
func String(text string) (string, error) {
	value, err := PartiallySanitize(protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(text))))
	if err != nil {
		return "", err
	}
	var out string
	if err := json.Unmarshal(value, &out); err != nil {
		return "", err
	}
	return strings.Clone(out), nil
}
