package provider

import (
	"bytes"
	"encoding/json"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Only inspect a value after the complete SDK JSON parser accepts it. Scanning
// top-level fields avoids Go's 10,000-depth limit in an unrelated future field.
// Duplicate keys replace earlier values, as JSON.parse does.
func sdkJSONTopLevelField(raw json.RawMessage, name string) json.RawMessage {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil
	}
	var selected json.RawMessage
	index := 1
	space := func() {
		for index < len(raw) && bytes.ContainsRune([]byte(" \t\r\n"), rune(raw[index])) {
			index++
		}
	}
	for {
		space()
		if index >= len(raw) || raw[index] == '}' {
			return selected
		}
		keyStart := index
		index = sdkJSONValueEnd(raw, index)
		key, _ := protocol.DecodeJavaScriptJSONString(raw[keyStart:index])
		space()
		index++
		space() // the validated object has a colon here
		valueStart := index
		index = sdkJSONValueEnd(raw, index)
		if string(utf16.Decode(key)) == name {
			selected = raw[valueStart:index]
		}
		space()
		if index >= len(raw) || raw[index] == '}' {
			return selected
		}
		index++ // the validated object has a comma here
	}
}

func sdkJSONValueEnd(raw []byte, index int) int {
	depth := 0
	quoted := false
	for index < len(raw) {
		unit := raw[index]
		if quoted {
			if unit == '\\' {
				index += 2
				continue
			}
			index++
			if unit == '"' {
				quoted = false
				if depth == 0 {
					return index
				}
			}
			continue
		}
		switch unit {
		case '"':
			quoted = true
		case '{', '[':
			depth++
		case '}', ']':
			if depth == 0 {
				return index
			}
			depth--
			if depth == 0 {
				return index + 1
			}
		case ',', ' ', '\t', '\r', '\n':
			if depth == 0 {
				return index
			}
		}
		index++
	}
	return index
}
