package protocol

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// JSON.parse preserves lone UTF-16 code units, whereas encoding/json replaces
// them. Tool argument strings must keep them both in parsed input and in the
// malformed-input feedback marker. The input has already passed json.Valid.
func decodeJSString(raw []byte) []uint16 {
	var units []uint16
	for index := 1; index < len(raw)-1; {
		if raw[index] == '\\' {
			index++
			switch raw[index] {
			case 'u':
				unit, _ := strconv.ParseUint(string(raw[index+1:index+5]), 16, 16)
				units = append(units, uint16(unit))
				index += 5
				continue
			case 'b':
				units = append(units, '\b')
			case 'f':
				units = append(units, '\f')
			case 'n':
				units = append(units, '\n')
			case 'r':
				units = append(units, '\r')
			case 't':
				units = append(units, '\t')
			default:
				units = append(units, uint16(raw[index]))
			}
			index++
			continue
		}
		r, count := utf8.DecodeRune(raw[index:])
		units = append(units, utf16.Encode([]rune{r})...)
		index += count
	}
	return units
}

// JavaScriptJSONSourceUTF16 makes an original JSON.parse string readable by a
// UTF-8 validator without replacing lone UTF-16 units or normalizing numbers.
// It is a source conversion, not validation or JSON.stringify. An invalid
// escape before a literal lone unit remains invalid after conversion.
func JavaScriptJSONSourceUTF16(units []uint16) string { return jsonSourceUTF16(units) }

// Escaping lone code units makes a UTF-16 JSON source readable by a UTF-8
// validator without replacing them. Outside a quoted JSON string that escape
// remains invalid, as the original JSON.parse input was.
func jsonSourceUTF16(units []uint16) string {
	var source strings.Builder
	for index := 0; index < len(units); index++ {
		unit := units[index]
		if utf16.IsSurrogate(rune(unit)) {
			if unit >= 0xd800 && unit <= 0xdbff && index+1 < len(units) && units[index+1] >= 0xdc00 && units[index+1] <= 0xdfff {
				source.WriteRune(utf16.DecodeRune(rune(unit), rune(units[index+1])))
				index++
			} else {
				// A literal lone unit after an odd number of backslashes was an
				// invalid escape in JSON.parse. Inserting only \\u here would
				// accidentally turn it into a valid escaped-backslash string.
				backslashes := 0
				for prior := index - 1; prior >= 0 && units[prior] == '\\'; prior-- {
					backslashes++
				}
				if backslashes%2 == 1 {
					source.WriteByte('x')
				}
				fmt.Fprintf(&source, `\u%04x`, unit)
			}
		} else {
			source.WriteRune(rune(unit))
		}
	}
	return source.String()
}

type jsJSONNormalizer struct {
	source string
	index  int
}

// ParseAndStringifyJavaScriptJSON implements JSON.parse followed by compact
// JSON.stringify for a UTF-8 JSON source. It preserves JavaScript's object key
// enumeration, duplicate-key replacement, double-number semantics, and UTF-16
// string code units. It does not alter application fields or protocol shape.
func ParseAndStringifyJavaScriptJSON(raw []byte) ([]byte, error) {
	var source json.RawMessage
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, err
	}
	return normalizeJSJSON(string(source)), nil
}

// StringifyValidatedJavaScriptJSON is for values already accepted by a
// JavaScript-compatible parser. It avoids encoding/json's unrelated 10,000
// nesting limit. Callers must validate the entire source before calling it;
// ordinary untrusted inputs use ParseAndStringifyJavaScriptJSON.
func StringifyValidatedJavaScriptJSON(raw []byte) []byte {
	return normalizeJSJSON(string(raw))
}

func normalizeJSJSON(source string) json.RawMessage {
	parser := jsJSONNormalizer{source: source}
	return json.RawMessage(parser.value())
}

func (parser *jsJSONNormalizer) whitespace() {
	for parser.index < len(parser.source) && strings.ContainsRune(" \t\r\n", rune(parser.source[parser.index])) {
		parser.index++
	}
}

func (parser *jsJSONNormalizer) quotedString() string {
	start := parser.index
	parser.index++
	for parser.source[parser.index] != '"' {
		if parser.source[parser.index] == '\\' {
			parser.index++
		}
		parser.index++
	}
	parser.index++
	return quoteUTF16(decodeJSString([]byte(parser.source[start:parser.index])))
}

func (parser *jsJSONNormalizer) value() string {
	parser.whitespace()
	switch parser.source[parser.index] {
	case '"':
		return parser.quotedString()
	case '[':
		parser.index++
		parser.whitespace()
		var values []string
		for parser.source[parser.index] != ']' {
			values = append(values, parser.value())
			parser.whitespace()
			if parser.source[parser.index] == ',' {
				parser.index++
				parser.whitespace()
			}
		}
		parser.index++
		return "[" + strings.Join(values, ",") + "]"
	case '{':
		parser.index++
		parser.whitespace()
		var keys []string
		values := make(map[string]string)
		for parser.source[parser.index] != '}' {
			key := parser.quotedString()
			parser.whitespace()
			parser.index++ // colon, proven by json.Valid
			value := parser.value()
			if _, exists := values[key]; !exists {
				keys = append(keys, key)
			}
			values[key] = value // JSON.parse retains the last duplicate value.
			parser.whitespace()
			if parser.source[parser.index] == ',' {
				parser.index++
				parser.whitespace()
			}
		}
		parser.index++
		// Object enumeration places array-index keys first in numeric
		// order. Other keys keep their first insertion position.
		sort.SliceStable(keys, func(left, right int) bool {
			leftIndex, leftIsIndex := jsArrayIndex(keys[left])
			rightIndex, rightIsIndex := jsArrayIndex(keys[right])
			if leftIsIndex && rightIsIndex {
				return leftIndex < rightIndex
			}
			return leftIsIndex && !rightIsIndex
		})
		entries := make([]string, len(keys))
		for index, key := range keys {
			entries[index] = key + ":" + values[key]
		}
		return "{" + strings.Join(entries, ",") + "}"
	default:
		start := parser.index
		for parser.index < len(parser.source) && !strings.ContainsRune(" \t\r\n,]}", rune(parser.source[parser.index])) {
			parser.index++
		}
		token := parser.source[start:parser.index]
		if token == "true" || token == "false" || token == "null" {
			return token
		}
		number, _ := strconv.ParseFloat(token, 64)
		if math.IsInf(number, 0) {
			return "null" // JSON.stringify(+-Infinity) is null.
		}
		return jsNumber(number)
	}
}

func jsArrayIndex(quoted string) (uint64, bool) {
	name := quoted[1 : len(quoted)-1]
	value, err := strconv.ParseUint(name, 10, 32)
	return value, err == nil && value < math.MaxUint32 && strconv.FormatUint(value, 10) == name
}
