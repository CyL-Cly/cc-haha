// Package ultracode contains Source's shared prompt matcher and instructions.
// Session state remains on the MainPrint owner; matching never grants consent.
package ultracode

import (
	"strings"
	"unicode/utf16"
)

const EnterReminder = "Ultracode is on: optimize for the most exhaustive, correct answer — not the fastest or cheapest. Use the Workflow tool on every substantive task; token cost is not a constraint. See the Workflow tool's **Ultracode** section and quality patterns. Solo only on conversational/trivial turns."
const StillOnReminder = "Ultracode is still on — use the Workflow tool; see its Ultracode section."
const ExitReminder = "Ultracode is off — the Workflow tool's standard opt-in rule applies again."
const KeywordReminder = "The user included the keyword \"ultracode\", opting this turn into multi-agent orchestration — use the Workflow tool to fulfill the request."

type Range struct {
	Word  string `json:"word"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

func word(c uint16) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}
func alpha(c uint16) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// Ranges uses JS UTF-16 offsets and the original closed opaque-span semantics.
// An unmatched opener does not hide the remaining prompt in Source.
func Ranges(text string) []Range {
	units := utf16.Encode([]rune(text))
	spans := [][2]int{}
	var open uint16
	start := 0
	closing := map[uint16]uint16{'`': '`', '"': '"', '<': '>', '{': '}', '[': ']', '(': ')', '\'': '\''}
	at := func(i int) uint16 {
		if i < 0 || i >= len(units) {
			return 0
		}
		return units[i]
	}
	for i, c := range units {
		if open != 0 {
			if c != closing[open] || open == '\'' && word(at(i+1)) {
				continue
			}
			spans = append(spans, [2]int{start, i + 1})
			open = 0
			continue
		}
		_, other := closing[c]
		if c == '<' && (alpha(at(i+1)) || at(i+1) == '/') || c == '\'' && !word(at(i-1)) || c != '<' && c != '\'' && other {
			open = c
			start = i
		}
	}
	result := []Range{}
	for i := 0; i+9 <= len(units); i++ {
		if word(at(i-1)) || word(at(i+9)) {
			continue
		}
		value := string(utf16.Decode(units[i : i+9]))
		if !strings.EqualFold(value, "ultracode") {
			continue
		}
		opaque := false
		for _, span := range spans {
			if i >= span[0] && i < span[1] {
				opaque = true
				break
			}
		}
		if opaque {
			continue
		}
		before, after := at(i-1), at(i+9)
		if before == '/' || before == '\\' || before == '-' || after == '/' || after == '\\' || after == '-' || after == '?' || after == '.' && word(at(i+10)) {
			continue
		}
		result = append(result, Range{value, i, i + 9})
		i += 8
	}
	return result
}
func HasKeyword(text string) bool { return len(Ranges(text)) > 0 }
