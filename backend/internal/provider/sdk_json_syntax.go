package provider

import (
	"encoding/json"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// PinnedJavaScriptJSONSyntaxMessage returns the original JSON.parse message
// as a quoted UTF-16-safe JSON string. A valid input returns nil, false. It is
// a diagnostic boundary, not a parser or a normalization step for values.
func PinnedJavaScriptJSONSyntaxMessage(input string) (json.RawMessage, bool) {
	units := sdkJSONSyntaxUnits(input)
	if len(units) == 0 {
		return nil, false
	}
	return protocol.QuoteJavaScriptUTF16(units), true
}

// This parser only diagnoses invalid JSON; it never builds or normalizes a
// business value. Its diagnostic grammar follows pinned Bun's JavaScriptCore
// JSON.parse. UTF-16 tokens keep lone code units in error.message too.
type sdkJSONDiagnosticParser struct {
	source []uint16
	index  int
	token  int
	text   []uint16
	err    []uint16
}

const (
	sdkJSONString = 256 + iota
	sdkJSONNumber
	sdkJSONIdentifier
	sdkJSONLiteral
)

func sdkJSONSyntaxUnits(data string) []uint16 {
	p := sdkJSONDiagnosticParser{source: utf16.Encode([]rune(data))}
	p.advance()
	p.value()
	if len(p.err) == 0 && p.token != 0 {
		p.fail("Unable to parse JSON string")
	}
	if len(p.err) == 0 {
		return nil
	}
	return append(utf16.Encode([]rune("JSON Parse error: ")), p.err...)
}

func (p *sdkJSONDiagnosticParser) fail(message string) {
	if len(p.err) == 0 {
		p.err = utf16.Encode([]rune(message))
	}
}
func (p *sdkJSONDiagnosticParser) failUnits(prefix string, units []uint16, suffix string) {
	if len(p.err) == 0 {
		p.err = append(utf16.Encode([]rune(prefix)), units...)
		p.err = append(p.err, utf16.Encode([]rune(suffix))...)
	}
}
func sdkJSONDigit(unit uint16) bool { return unit >= '0' && unit <= '9' }
func sdkJSONHex(unit uint16) bool {
	return sdkJSONDigit(unit) || unit >= 'a' && unit <= 'f' || unit >= 'A' && unit <= 'F'
}
func sdkJSONIdentifierUnit(unit uint16) bool {
	return unit >= 'a' && unit <= 'z' || unit >= 'A' && unit <= 'Z' || unit == '_' || unit == '$'
}

func (p *sdkJSONDiagnosticParser) advance() {
	if len(p.err) > 0 {
		return
	}
	for p.index < len(p.source) {
		u := p.source[p.index]
		if u != ' ' && u != '\t' && u != '\r' && u != '\n' {
			break
		}
		p.index++
	}
	p.text = nil
	if p.index == len(p.source) {
		p.token = 0
		return
	}
	u := p.source[p.index]
	switch u {
	case '"':
		p.token = sdkJSONString
		p.index++
		for p.index < len(p.source) {
			c := p.source[p.index]
			p.index++
			if c == '"' {
				return
			}
			if c < 0x20 {
				p.fail("Unterminated string")
				return
			}
			if c != '\\' {
				continue
			}
			if p.index == len(p.source) {
				p.fail("Unterminated string")
				return
			}
			c = p.source[p.index]
			p.index++
			switch c {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
			case 'u':
				if len(p.source)-p.index < 4 {
					p.fail(`\u must be followed by 4 hex digits`)
					return
				}
				units := p.source[p.index : p.index+4]
				for _, unit := range units {
					if !sdkJSONHex(unit) {
						p.failUnits(`"\u`, units, `" is not a valid unicode escape`)
						return
					}
				}
				p.index += 4
			default:
				p.failUnits("Invalid escape character ", []uint16{c}, "")
				return
			}
		}
		p.fail("Unterminated string")
		return
	case '{', '}', '[', ']', ':', ',', '.', '(', ')':
		p.token = int(u)
		p.index++
		return
	case '\'':
		p.fail("Single quotes (') are not allowed in JSON")
		return
	}
	if u == '-' || sdkJSONDigit(u) {
		p.token = sdkJSONNumber
		if u == '-' {
			p.index++
			if p.index == len(p.source) || !sdkJSONDigit(p.source[p.index]) {
				p.fail("Invalid number")
				return
			}
		}
		if p.source[p.index] == '0' {
			p.index++
		} else {
			for p.index < len(p.source) && sdkJSONDigit(p.source[p.index]) {
				p.index++
			}
		}
		if p.index < len(p.source) && p.source[p.index] == '.' {
			p.index++
			if p.index == len(p.source) || !sdkJSONDigit(p.source[p.index]) {
				p.fail("Invalid digits after decimal point")
				return
			}
			for p.index < len(p.source) && sdkJSONDigit(p.source[p.index]) {
				p.index++
			}
		}
		if p.index < len(p.source) && (p.source[p.index] == 'e' || p.source[p.index] == 'E') {
			exponent := p.index
			p.index++
			if p.index < len(p.source) && (p.source[p.index] == '+' || p.source[p.index] == '-') {
				p.index++
			}
			if p.index == len(p.source) || !sdkJSONDigit(p.source[p.index]) {
				p.index = exponent
				return
			}
			for p.index < len(p.source) && sdkJSONDigit(p.source[p.index]) {
				p.index++
			}
		}
		return
	}
	for _, literal := range []string{"true", "false", "null"} {
		units := []uint16{}
		for _, c := range literal {
			units = append(units, uint16(c))
		}
		if len(p.source)-p.index >= len(units) {
			equal := true
			for index, c := range units {
				if p.source[p.index+index] != c {
					equal = false
					break
				}
			}
			if equal {
				p.token = sdkJSONLiteral
				p.index += len(units)
				return
			}
		}
	}
	if sdkJSONIdentifierUnit(u) {
		p.token = sdkJSONIdentifier
		start := p.index
		for p.index < len(p.source) && (sdkJSONIdentifierUnit(p.source[p.index]) || sdkJSONDigit(p.source[p.index])) {
			p.index++
		}
		p.text = p.source[start:p.index]
		return
	}
	p.failUnits("Unrecognized token '", []uint16{u}, "'")
}

func (p *sdkJSONDiagnosticParser) value() {
	if len(p.err) > 0 {
		return
	}
	switch p.token {
	case sdkJSONString, sdkJSONNumber, sdkJSONLiteral:
		p.advance()
	case '{':
		p.advance()
		if p.token == '}' {
			p.advance()
			return
		}
		if p.token != sdkJSONString {
			p.fail("Expected '}'")
			return
		}
		for len(p.err) == 0 {
			p.advance()
			if p.token != ':' {
				p.fail("Expected ':' before value in object property definition")
				return
			}
			p.advance()
			p.value()
			if len(p.err) > 0 {
				return
			}
			if p.token == '}' {
				p.advance()
				return
			}
			if p.token != ',' {
				p.fail("Expected '}'")
				return
			}
			p.advance()
			if p.token != sdkJSONString {
				p.fail("Property name must be a string literal")
				return
			}
		}
	case '[':
		p.advance()
		if p.token == ']' {
			p.advance()
			return
		}
		for len(p.err) == 0 {
			p.value()
			if len(p.err) > 0 {
				return
			}
			if p.token == ']' {
				p.advance()
				return
			}
			if p.token != ',' {
				p.fail("Expected ']'")
				return
			}
			p.advance()
			if p.token == ']' {
				p.fail("Unexpected comma at the end of array expression")
				return
			}
		}
	case sdkJSONIdentifier:
		p.failUnits(`Unexpected identifier "`, p.text, `"`)
	case 0:
		p.fail("Unexpected EOF")
	default:
		p.failUnits("Unexpected token '", []uint16{uint16(p.token)}, "'")
	}
}
