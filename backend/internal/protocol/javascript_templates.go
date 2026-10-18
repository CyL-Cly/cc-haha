package protocol

import (
	"encoding/json"
	"unicode/utf16"
)

// ReplaceJavaScriptLiteralJSON preserves String.replace's function-replacement
// behavior, including literal dollar patterns and lazy replacement producers.
func ReplaceJavaScriptLiteralJSON(value json.RawMessage, needle string, replace func() (json.RawMessage, error)) (json.RawMessage, error) {
	return replaceJavaScriptLiteralJSON(value, needle, func(_ []uint16, _, _ int) (json.RawMessage, error) { return replace() })
}

// ReplaceJavaScriptStringJSON handles the replacement-string dollar patterns
// for a literal global regexp without capture groups.
func ReplaceJavaScriptStringJSON(value json.RawMessage, needle string, replacement json.RawMessage) (json.RawMessage, error) {
	inserted, err := DecodeJavaScriptJSONString(replacement)
	if err != nil {
		return nil, err
	}
	return replaceJavaScriptLiteralJSON(value, needle, func(original []uint16, at, length int) (json.RawMessage, error) {
		result := []uint16{}
		for i := 0; i < len(inserted); i++ {
			if inserted[i] == '$' && i+1 < len(inserted) {
				switch inserted[i+1] {
				case '$':
					result = append(result, '$')
					i++
					continue
				case '&':
					result = append(result, original[at:at+length]...)
					i++
					continue
				case '`':
					result = append(result, original[:at]...)
					i++
					continue
				case '\'':
					result = append(result, original[at+length:]...)
					i++
					continue
				}
			}
			result = append(result, inserted[i])
		}
		return QuoteJavaScriptUTF16(result), nil
	})
}

func replaceJavaScriptLiteralJSON(value json.RawMessage, needle string, replace func([]uint16, int, int) (json.RawMessage, error)) (json.RawMessage, error) {
	units, err := DecodeJavaScriptJSONString(value)
	if err != nil {
		return nil, err
	}
	pattern := utf16.Encode([]rune(needle))
	result := make([]uint16, 0, len(units))
	for index := 0; index < len(units); {
		match := len(pattern) > 0 && index+len(pattern) <= len(units)
		if match {
			for offset, unit := range pattern {
				if units[index+offset] != unit {
					match = false
					break
				}
			}
		}
		if !match {
			result = append(result, units[index])
			index++
			continue
		}
		value, err := replace(units, index, len(pattern))
		if err != nil {
			return nil, err
		}
		inserted, err := DecodeJavaScriptJSONString(value)
		if err != nil {
			return nil, err
		}
		result = append(result, inserted...)
		index += len(pattern)
	}
	return QuoteJavaScriptUTF16(result), nil
}

// ReplaceJavaScriptTemplatesJSON retains code-unit identity across the simple
// ${...} replacement callbacks used by the original plugin consumers. A nil
// replacement keeps the match literal. Inserted text is not scanned again.
func ReplaceJavaScriptTemplatesJSON(value json.RawMessage, prefix string, replace func(json.RawMessage) (json.RawMessage, error)) (json.RawMessage, error) {
	units, err := DecodeJavaScriptJSONString(value)
	if err != nil {
		return nil, err
	}
	needle := utf16.Encode([]rune(prefix))
	result := make([]uint16, 0, len(units))
	for index := 0; index < len(units); {
		match := len(needle) > 0 && index+len(needle) <= len(units)
		if match {
			for offset, unit := range needle {
				if units[index+offset] != unit {
					match = false
					break
				}
			}
		}
		end := index + len(needle)
		if match {
			for end < len(units) && units[end] != '}' {
				end++
			}
			match = end < len(units) && end > index+len(needle)
		}
		if !match {
			result = append(result, units[index])
			index++
			continue
		}
		replacement, err := replace(QuoteJavaScriptUTF16(units[index+len(needle) : end]))
		if err != nil {
			return nil, err
		}
		if replacement == nil {
			result = append(result, units[index:end+1]...)
		} else {
			inserted, err := DecodeJavaScriptJSONString(replacement)
			if err != nil {
				return nil, err
			}
			result = append(result, inserted...)
		}
		index = end + 1
	}
	return QuoteJavaScriptUTF16(result), nil
}
