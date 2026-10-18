package insights

import (
	"encoding/json"
	"errors"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func trimUnits(v []uint16) []uint16 {
	for len(v) > 0 && protocol.IsJavaScriptWhitespace(rune(v[0])) {
		v = v[1:]
	}
	for len(v) > 0 && protocol.IsJavaScriptWhitespace(rune(v[len(v)-1])) {
		v = v[:len(v)-1]
	}
	return v
}
func asciiMatch(v []uint16, index int, text string) bool {
	if index+len(text) > len(v) {
		return false
	}
	for i, c := range []byte(text) {
		u := v[index+i]
		if u >= 'A' && u <= 'Z' {
			u += 'a' - 'A'
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if u != uint16(c) {
			return false
		}
	}
	return true
}
func openTag(v []uint16, index int, name string) (int, bool) {
	prefix := "<" + name
	if !asciiMatch(v, index, prefix) {
		return 0, false
	}
	stop := index + len(prefix)
	if stop >= len(v) {
		return 0, false
	}
	if v[stop] == '>' {
		return stop + 1, true
	}
	if !protocol.IsJavaScriptWhitespace(rune(v[stop])) {
		return 0, false
	}
	for stop < len(v) && v[stop] != '>' {
		stop++
	}
	return stop + 1, stop < len(v)
}
func extractTag(v []uint16, name string) []uint16 {
	if len(trimUnits(v)) == 0 {
		return nil
	}
	last := 0
	for start := 0; start < len(v); start++ {
		open, ok := openTag(v, start, name)
		if !ok {
			continue
		}
		end := open
		for end < len(v) && !asciiMatch(v, end, "</"+name+">") {
			end++
		}
		if end == len(v) {
			continue
		}
		depth := 0
		for i := last; i < start; i++ {
			if stop, ok := openTag(v, i, name); ok && stop <= start {
				depth++
				i = stop - 1
				continue
			}
			if asciiMatch(v, i, "</"+name+">") {
				depth--
				i += len(name) + 2
			}
		}
		if depth == 0 && end > open {
			return v[open:end]
		}
		last = end + len(name) + 3
		start = last - 1
	}
	return nil
}
func skipPrompt(v []uint16) bool {
	if len(v) >= len("[Request interrupted by user") && string(utf16.Decode(v[:len("[Request interrupted by user")])) == "[Request interrupted by user" {
		for _, u := range v[len("[Request interrupted by user"):] {
			if u == ']' {
				return true
			}
		}
	}
	index := 0
	for index < len(v) && protocol.IsJavaScriptWhitespace(rune(v[index])) {
		index++
	}
	if index+1 >= len(v) || v[index] != '<' || v[index+1] < 'a' || v[index+1] > 'z' {
		return false
	}
	index += 2
	for index < len(v) && (v[index] >= 'a' && v[index] <= 'z' || v[index] >= 'A' && v[index] <= 'Z' || v[index] >= '0' && v[index] <= '9' || v[index] == '_' || v[index] == '-') {
		index++
	}
	return index < len(v) && (v[index] == '>' || protocol.IsJavaScriptWhitespace(rune(v[index])))
}

// FirstPrompt ports the full CLI first-meaningful-user selector, retaining
// preceding IDE-block skipping, custom slash arguments and UTF-16 truncation.
func FirstPrompt(records []protocol.JSONObject, builtins map[string]bool) (json.RawMessage, error) {
	value, err := FirstMeaningfulPrompt(records, func(name string) (bool, error) { return builtins[name], nil })
	if err == nil && value == nil {
		return raw("No prompt"), nil
	}
	return value, err
}

// FirstMeaningfulPrompt preserves the selector's undefined result separately
// from a real user saying "No prompt". The compiled-name lookup stays lazy,
// as in the original helper's command-tag branch.
func FirstMeaningfulPrompt(records []protocol.JSONObject, isBuiltin func(string) (bool, error)) (json.RawMessage, error) {
	for _, record := range records {
		if record.String("type") != "user" || truthy(record.Get("isMeta")) || truthy(record.Get("isCompactSummary")) {
			continue
		}
		body, _ := record.Object("message")
		content := body.Get("content")
		if !truthy(content) {
			continue
		}
		texts := []json.RawMessage{}
		if len(content) > 0 && content[0] == '"' {
			texts = append(texts, content)
		} else if len(content) > 0 && content[0] == '[' {
			var blocks []json.RawMessage
			if err := json.Unmarshal(content, &blocks); err != nil {
				return nil, err
			}
			for _, block := range blocks {
				if string(block) == "null" {
					return nil, errors.New("null is not an object")
				}
				o, _ := object(block)
				if o.String("type") == "text" && truthy(o.Get("text")) {
					texts = append(texts, o.Get("text"))
				}
			}
		}
		for _, text := range texts {
			units, err := protocol.DecodeJavaScriptJSONString(text)
			if err != nil {
				return nil, err
			}
			if len(units) == 0 {
				continue
			}
			selected := units
			if name := extractTag(units, "command-name"); len(name) > 0 {
				plain := name
				if len(plain) > 0 && plain[0] == '/' {
					plain = plain[1:]
				}
				if isBuiltin == nil {
					return nil, errors.New("meaningful prompt requires compiled builtin names")
				}
				builtin, err := isBuiltin(string(utf16.Decode(plain)))
				if err != nil {
					return nil, err
				}
				if builtin {
					continue
				}
				args := trimUnits(extractTag(units, "command-args"))
				if len(args) == 0 {
					continue
				}
				selected = append(append(append([]uint16{}, name...), ' '), args...)
			} else if bash := extractTag(units, "bash-input"); len(bash) > 0 {
				selected = append([]uint16{'!', ' '}, bash...)
			} else if skipPrompt(units) {
				continue
			}
			normalized := append([]uint16{}, selected...)
			for i, u := range normalized {
				if u == '\n' {
					normalized[i] = ' '
				}
			}
			normalized = trimUnits(normalized)
			if len(normalized) > 200 {
				normalized = trimUnits(normalized[:200])
				normalized = append(normalized, 0x2026)
			}
			return protocol.QuoteJavaScriptUTF16(normalized), nil
		}
	}
	return nil, nil
}
