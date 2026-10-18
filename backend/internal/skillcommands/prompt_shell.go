package skillcommands

import (
	"encoding/json"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dlclark/regexp2/v2"
)

type PromptShellMatch struct {
	Pattern, Command string
	PatternJSON      json.RawMessage
	CommandJSON      json.RawMessage
}

// Preserve the two matchAll passes and their order. Inline matches nested in
// a block are intentionally retained, just as in the original Promise.all.
func PromptShellMatches(text string) ([]PromptShellMatch, error) {
	value, _ := json.Marshal(text)
	return PromptShellMatchesJSON(value)
}

func PromptShellMatchesJSON(text json.RawMessage) ([]PromptShellMatch, error) {
	units, err := protocol.DecodeJavaScriptJSONString(text)
	if err != nil {
		return nil, err
	}
	runes := make([]rune, len(units))
	for i, unit := range units {
		runes[i] = rune(unit)
	}
	patterns := []*regexp2.Regexp{regexp2.MustCompile("```!\\s*\\n?([\\s\\S]*?)\\n?```", regexp2.ECMAScript)}
	if strings.Contains(string(runes), "!`") {
		patterns = append(patterns, regexp2.MustCompile("(?<=^|\\s)!`([^`]+)`", regexp2.ECMAScript|regexp2.Multiline))
	}
	out := []PromptShellMatch{}
	for _, pattern := range patterns {
		match, e := pattern.FindRunesMatch(runes)
		for match != nil && e == nil {
			command := match.Groups()[1].Runes()
			for len(command) > 0 && invocationWhitespace(uint16(command[0])) {
				command = command[1:]
			}
			for len(command) > 0 && invocationWhitespace(uint16(command[len(command)-1])) {
				command = command[:len(command)-1]
			}
			if len(command) > 0 {
				matched := units[match.RuneIndex : match.RuneIndex+match.RuneLength]
				commandUnits := make([]uint16, len(command))
				for i, unit := range command {
					commandUnits[i] = uint16(unit)
				}
				out = append(out, PromptShellMatch{Pattern: string(utf16.Decode(matched)), Command: string(utf16.Decode(commandUnits)), PatternJSON: protocol.QuoteJavaScriptUTF16(matched), CommandJSON: protocol.QuoteJavaScriptUTF16(commandUnits)})
			}
			match, e = pattern.FindNextMatch(match)
		}
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}

// ReplacePromptShellMatchJSON performs the original first String.replace with
// a function result. Preserve surrounding code units and completion order.
func ReplacePromptShellMatchJSON(text json.RawMessage, match PromptShellMatch, output string) (json.RawMessage, error) {
	units, err := protocol.DecodeJavaScriptJSONString(text)
	if err != nil {
		return nil, err
	}
	needle, err := protocol.DecodeJavaScriptJSONString(match.PatternJSON)
	if err != nil {
		return nil, err
	}
	for at := 0; len(needle) > 0 && at+len(needle) <= len(units); at++ {
		found := true
		for i, unit := range needle {
			if units[at+i] != unit {
				found = false
				break
			}
		}
		if found {
			result := append([]uint16{}, units[:at]...)
			result = append(result, utf16.Encode([]rune(output))...)
			result = append(result, units[at+len(needle):]...)
			return protocol.QuoteJavaScriptUTF16(result), nil
		}
	}
	return protocol.QuoteJavaScriptUTF16(units), nil
}
