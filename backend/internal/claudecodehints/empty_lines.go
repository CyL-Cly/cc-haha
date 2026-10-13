package claudecodehints

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// StripEmptyLines preserves nonempty lines byte-for-unit, including indentation
// and trailing tabs/spaces. Bash/PS trimEnd their accumulator before this step.
func StripEmptyLines(raw json.RawMessage) (json.RawMessage, error) {
	units, e := protocol.DecodeJavaScriptJSONString(raw)
	if e != nil {
		return nil, e
	}
	lines := [][]uint16{}
	start := 0
	for i, u := range units {
		if u == '\n' {
			lines = append(lines, units[start:i])
			start = i + 1
		}
	}
	lines = append(lines, units[start:])
	empty := func(line []uint16) bool {
		for _, u := range line {
			if !protocol.IsJavaScriptWhitespace(rune(u)) {
				return false
			}
		}
		return true
	}
	first, last := 0, len(lines)
	for first < last && empty(lines[first]) {
		first++
	}
	for last > first && empty(lines[last-1]) {
		last--
	}
	out := []uint16{}
	for i := first; i < last; i++ {
		if i > first {
			out = append(out, '\n')
		}
		out = append(out, lines[i]...)
	}
	return protocol.QuoteJavaScriptUTF16(out), nil
}
