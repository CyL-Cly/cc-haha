// Package claudecodehints implements the shell's model-invisible hints side channel.
package claudecodehints

import (
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dlclark/regexp2/v2"
)

type Hint struct {
	Version       int             `json:"v"`
	Type          string          `json:"type"`
	Value         json.RawMessage `json:"value"`
	SourceCommand json.RawMessage `json:"sourceCommand"`
}
type Extracted struct {
	Hints    []Hint          `json:"hints"`
	Stripped json.RawMessage `json:"stripped"`
}

const whitespace = `\x09-\x0d\x20\xa0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000\ufeff`

// JS multiline anchors include CR and both Unicode line terminators; regexp2's
// native multiline anchors only include LF. Subject offsets are UTF-16 units.
var tag = regexp2.MustCompile(`(?:\A|(?<=[\n\r\u2028\u2029]))[ \t]*<claude-code-hint[`+whitespace+`]+([^>]*?)[`+whitespace+`]*/>[ \t]*(?=\z|[\n\r\u2028\u2029])`, regexp2.ECMAScript)
var attribute = regexp2.MustCompile(`([a-zA-Z0-9_]+)=(?:"([^"]*)"|([^`+whitespace+`/>]+))`, regexp2.ECMAScript)

func unitRunes(units []uint16) []rune {
	r := make([]rune, len(units))
	for i, u := range units {
		r[i] = rune(u)
	}
	return r
}
func firstCommand(command []uint16) json.RawMessage {
	start, end := 0, len(command)
	for start < end && protocol.IsJavaScriptWhitespace(rune(command[start])) {
		start++
	}
	for end > start && protocol.IsJavaScriptWhitespace(rune(command[end-1])) {
		end--
	}
	for i := start; i < end; i++ {
		if protocol.IsJavaScriptWhitespace(rune(command[i])) {
			end = i
			break
		}
	}
	return protocol.QuoteJavaScriptUTF16(command[start:end])
}
func numberOne(value []uint16) bool {
	text := strings.TrimFunc(string(utf16.Decode(value)), protocol.IsJavaScriptWhitespace)
	if len(text) > 2 && text[0] == '0' {
		base := 0
		switch text[1] {
		case 'x', 'X':
			base = 16
		case 'b', 'B':
			base = 2
		case 'o', 'O':
			base = 8
		}
		if base != 0 {
			if strings.ContainsAny(text[2:], "+-_") {
				return false
			}
			v, e := strconv.ParseUint(text[2:], base, 64)
			return e == nil && v == 1
		}
	}
	if strings.Contains(text, "_") {
		return false
	}
	v, e := strconv.ParseFloat(text, 64)
	return e == nil && v == 1
}

func Extract(output, command json.RawMessage) (Extracted, error) {
	units, err := protocol.DecodeJavaScriptJSONString(output)
	if err != nil {
		return Extracted{}, err
	}
	cmd, err := protocol.DecodeJavaScriptJSONString(command)
	if err != nil {
		return Extracted{}, err
	}
	result := Extracted{Hints: []Hint{}, Stripped: protocol.QuoteJavaScriptUTF16(units)}
	if !strings.Contains(string(unitRunes(units)), "<claude-code-hint") {
		return result, nil
	}
	match, err := tag.FindRunesMatch(unitRunes(units))
	if err != nil {
		return result, err
	}
	if match == nil {
		return result, nil
	}
	stripped := make([]uint16, 0, len(units))
	cursor := 0
	for match != nil {
		stripped = append(stripped, units[cursor:match.RuneIndex]...)
		cursor = match.RuneIndex + match.RuneLength
		line := units[match.RuneIndex:cursor]
		attrs := map[string][]uint16{}
		attr, e := attribute.FindRunesMatch(unitRunes(line))
		if e != nil {
			return result, e
		}
		for attr != nil {
			key := attr.GroupByNumber(1)
			value := attr.GroupByNumber(2)
			if len(value.Captures) == 0 {
				value = attr.GroupByNumber(3)
			}
			attrs[string(unitRunes(line[key.RuneIndex:key.RuneIndex+key.RuneLength]))] = append([]uint16{}, line[value.RuneIndex:value.RuneIndex+value.RuneLength]...)
			attr, e = attribute.FindNextMatch(attr)
			if e != nil {
				return result, e
			}
		}
		if numberOne(attrs["v"]) && string(unitRunes(attrs["type"])) == "plugin" && len(attrs["value"]) > 0 {
			result.Hints = append(result.Hints, Hint{1, "plugin", protocol.QuoteJavaScriptUTF16(attrs["value"]), firstCommand(cmd)})
		}
		match, err = tag.FindNextMatch(match)
		if err != nil {
			return result, err
		}
	}
	stripped = append(stripped, units[cursor:]...)
	collapsed := make([]uint16, 0, len(stripped))
	newlines := 0
	for _, u := range stripped {
		if u == '\n' {
			newlines++
			if newlines > 2 {
				continue
			}
		} else {
			newlines = 0
		}
		collapsed = append(collapsed, u)
	}
	result.Stripped = protocol.QuoteJavaScriptUTF16(collapsed)
	return result, nil
}

func Clone(h Hint) Hint {
	h.Value = append(json.RawMessage{}, h.Value...)
	h.SourceCommand = append(json.RawMessage{}, h.SourceCommand...)
	return h
}
