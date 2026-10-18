package terminalsetup

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// The scanner retains error tokens and physical spans independently from their
// recovered value, as required by jsonc-parser's parser and formatter.
func jsonTokens(text string) []jsonToken {
	var tokens []jsonToken
	digit := func(i int) bool { return i < len(text) && text[i] >= '0' && text[i] <= '9' }
	for i := 0; i < len(text); {
		token := jsonToken{start: i}
		switch text[i] {
		case ' ', '\t', '\r', '\n':
			i++
			continue
		case '"':
			token.kind = 's'
			i++
			var units []uint16
			closed := false
			for i < len(text) {
				character := text[i]
				if character == '"' {
					i++
					closed = true
					break
				}
				if character == '\r' || character == '\n' {
					break
				}
				if character == '\\' {
					i++
					if i == len(text) {
						break
					}
					escape := text[i]
					i++
					switch escape {
					case '"', '\\', '/':
						units = append(units, uint16(escape))
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
					case 'u':
						value, digits := uint16(0), 0
						for digits < 4 && i < len(text) {
							character := text[i]
							var hex byte
							if character >= '0' && character <= '9' {
								hex = character - '0'
							} else if character >= 'A' && character <= 'F' {
								hex = character - 'A' + 10
							} else if character >= 'a' && character <= 'f' {
								hex = character - 'a' + 10
							} else {
								break
							}
							value = value*16 + uint16(hex)
							i++
							digits++
						}
						if digits == 4 {
							units = append(units, value)
						} else {
							token.invalid = true
						}
					default:
						token.invalid = true
					}
					continue
				}
				if character < 0x20 {
					token.invalid = true
				}
				runeValue, size := utf8.DecodeRuneInString(text[i:])
				units = append(units, utf16.Encode([]rune{runeValue})...)
				i += size
			}
			token.invalid = token.invalid || !closed
			token.raw = protocol.QuoteJavaScriptUTF16(units)
		case '/', '{', '}', '[', ']', ':', ',':
			token.kind = text[i]
			i++
			if token.kind == '/' {
				token.kind = 'u'
				if i < len(text) && text[i] == '/' {
					token.kind = 'l'
					for i < len(text) && text[i] != '\r' && text[i] != '\n' {
						i++
					}
				} else if i < len(text) && text[i] == '*' {
					token.kind = 'b'
					i++
					for i+1 < len(text) && text[i:i+2] != "*/" {
						i++
					}
					if i+1 < len(text) {
						i += 2
					} else {
						i = len(text)
						token.invalid = true
					}
				}
			}
		default:
			if text[i] == '-' || digit(i) {
				if text[i] == '-' {
					i++
				}
				if !digit(i) {
					token.kind = 'u'
					break
				}
				token.kind = 'n'
				if text[i] == '0' {
					i++
				} else {
					for digit(i) {
						i++
					}
				}
				if i < len(text) && text[i] == '.' {
					i++
					if !digit(i) {
						token.invalid = true
					}
					for digit(i) {
						i++
					}
				}
				endValue := i
				if !token.invalid && i < len(text) && (text[i] == 'e' || text[i] == 'E') {
					i++
					if i < len(text) && (text[i] == '+' || text[i] == '-') {
						i++
					}
					if !digit(i) {
						token.invalid = true
					} else {
						for digit(i) {
							i++
						}
						endValue = i
					}
				}
				value, err := strconv.ParseFloat(text[token.start:endValue], 64)
				if err != nil && !math.IsInf(value, 0) {
					value = 0
				}
				if math.IsInf(value, 0) {
					token.raw = json.RawMessage("null")
				} else {
					token.raw, _ = json.Marshal(value)
				}
				break
			}
			for i < len(text) && !strings.ContainsRune(" \t\r\n{}[],:/\"", rune(text[i])) {
				i++
			}
			if i == token.start {
				i++
			}
			value := text[token.start:i]
			token.kind = 'u'
			if value == "null" || value == "true" || value == "false" {
				token.kind = 'v'
				token.raw = json.RawMessage(value)
			}
		}
		token.end = i
		tokens = append(tokens, token)
	}
	return tokens
}
