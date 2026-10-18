// Package sessiontitle retains Source sessionTitleText's user-facing strings,
// including UTF-16 units that cannot be represented in a Go string.
package sessiontitle

import (
	"encoding/json"
	"fmt"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/charmbracelet/x/ansi"
	"github.com/dlclark/regexp2/v2"
	"github.com/rivo/uniseg"
)

const whitespace = `[\t\n\v\f\r \u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000\ufeff]`

var block = regexp2.MustCompile(`<([a-z][\w-]*)(?:`+whitespace+`[^>]*)?>[\s\S]*?</\1>\n?`, regexp2.ECMAScript)
var markup = regexp2.MustCompile(`<([a-z][\w-]*)(?:`+whitespace+`[^>]*)?>[\s\S]*?</\1>`, regexp2.ECMAScript)

func units(raw json.RawMessage) ([]uint16, error) { return protocol.DecodeJavaScriptJSONString(raw) }
func runes(text []uint16) []rune {
	result := make([]rune, len(text))
	for i, u := range text {
		result[i] = rune(u)
	}
	return result
}
func literal(text string) []uint16 { return utf16.Encode([]rune(text)) }
func trim(text []uint16) []uint16 {
	for len(text) > 0 && protocol.IsJavaScriptWhitespace(rune(text[0])) {
		text = text[1:]
	}
	for len(text) > 0 && protocol.IsJavaScriptWhitespace(rune(text[len(text)-1])) {
		text = text[:len(text)-1]
	}
	return text
}
func normalized(text []uint16) []uint16 {
	result := []uint16{}
	space := false
	for _, u := range text {
		if protocol.IsJavaScriptWhitespace(rune(u)) {
			space = len(result) > 0
			continue
		}
		if space {
			result = append(result, ' ')
			space = false
		}
		result = append(result, u)
	}
	return result
}
func replace(text, needle, replacement []uint16) []uint16 {
	result := []uint16{}
	for index := 0; index < len(text); {
		match := index+len(needle) <= len(text)
		if match {
			for j := range needle {
				if text[index+j] != needle[j] {
					match = false
					break
				}
			}
		}
		if match {
			result = append(result, replacement...)
			index += len(needle)
		} else {
			result = append(result, text[index])
			index++
		}
	}
	return result
}
func extract(text []uint16, tag string) ([]uint16, error) {
	expression, err := regexp2.Compile(`<`+tag+`(?:`+whitespace+`[^>]*)?>([\s\S]*?)</`+tag+`>`, regexp2.ECMAScript|regexp2.IgnoreCase)
	if err != nil {
		return nil, err
	}
	match, err := expression.FindRunesMatch(runes(text))
	if err != nil || match == nil {
		return nil, err
	}
	group := match.GroupByNumber(1)
	value := trim(text[group.RuneIndex : group.RuneIndex+group.RuneLength])
	if len(value) == 0 {
		return nil, nil
	}
	for _, pair := range [][2]string{{"&lt;", "<"}, {"&gt;", ">"}, {"&quot;", "\""}, {"&#39;", "'"}, {"&amp;", "&"}} {
		value = replace(value, literal(pair[0]), literal(pair[1]))
	}
	return value, nil
}

// CleanRaw is cleanSessionTitleSource, not an XML parser. Its original regex
// ignores unmatched wrappers, strips complete lowercase blocks, and decodes
// entities only in the command breadcrumbs.
func CleanRaw(raw json.RawMessage) (json.RawMessage, error) {
	text, err := units(raw)
	if err != nil {
		return nil, err
	}
	command, err := extract(text, "command-name")
	if err != nil {
		return nil, err
	}
	args, err := extract(text, "command-args")
	if err != nil {
		return nil, err
	}
	if len(command) > 0 {
		if len(args) > 0 {
			command = append(append(command, ' '), args...)
		}
		return protocol.QuoteJavaScriptUTF16(normalized(command)), nil
	}
	match, err := block.FindRunesMatch(runes(text))
	if err != nil {
		return nil, err
	}
	result := []uint16{}
	cursor := 0
	for match != nil {
		result = append(result, text[cursor:match.RuneIndex]...)
		result = append(result, ' ')
		cursor = match.RuneIndex + match.RuneLength
		match, err = block.FindNextMatch(match)
		if err != nil {
			return nil, err
		}
	}
	result = append(result, text[cursor:]...)
	return protocol.QuoteJavaScriptUTF16(normalized(result)), nil
}
func NormalizeRaw(raw json.RawMessage) (json.RawMessage, bool, error) {
	cleaned, err := CleanRaw(raw)
	if err != nil {
		return nil, false, err
	}
	text, _ := units(cleaned)
	if len(text) == 0 || len(text) > 80 {
		return nil, false, nil
	}
	present, err := markup.MatchRunes(runes(text))
	if err != nil {
		return nil, false, fmt.Errorf("session title markup: %w", err)
	}
	if present {
		return nil, false, nil
	}
	return cleaned, true, nil
}

// TruncateRaw follows truncateToWidth's grapheme loop while mapping each
// displayed segment back to the original UTF-16 units (including lone ones).
func TruncateRaw(raw json.RawMessage, width int) (json.RawMessage, error) {
	text, err := units(raw)
	if err != nil {
		return nil, err
	}
	displayed := string(utf16.Decode(text))
	if titleDisplayWidth(text) <= width {
		return append(json.RawMessage(nil), raw...), nil
	}
	if width <= 1 {
		return protocol.QuoteJavaScriptUTF16([]uint16{'…'}), nil
	}
	segments := uniseg.NewGraphemes(displayed)
	used, offset := 0, 0
	result := []uint16{}
	for segments.Next() {
		segment := segments.Str()
		length := len(utf16.Encode([]rune(segment)))
		size := titleDisplayWidth(text[offset : offset+length])
		if used+size > width-1 {
			break
		}
		result = append(result, text[offset:offset+length]...)
		offset += length
		used += size
	}
	return protocol.QuoteJavaScriptUTF16(append(result, '…')), nil
}

func titleDisplayWidth(text []uint16) int {
	// Bun.stringWidth treats lone surrogate units as invisible. Preserve them
	// in the returned value while excluding them from width calculation.
	visible := []uint16{}
	for i := 0; i < len(text); i++ {
		u := text[i]
		if u >= 0xd800 && u <= 0xdbff && i+1 < len(text) && text[i+1] >= 0xdc00 && text[i+1] <= 0xdfff {
			visible = append(visible, u, text[i+1])
			i++
			continue
		}
		if u >= 0xd800 && u <= 0xdfff {
			continue
		}
		visible = append(visible, u)
	}
	return ansi.StringWidth(string(utf16.Decode(visible)))
}
