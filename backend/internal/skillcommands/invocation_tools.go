package skillcommands

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// ParseInvocationTools is the original parseToolListFromCLI grammar used by
// prompt expansion. It does not canonicalize names or turn a command grant
// into a base-tool registry selection.
func ParseInvocationTools(raw json.RawMessage) ([]json.RawMessage, error) {
	items := []json.RawMessage{}
	if len(raw) > 0 {
		if e := json.Unmarshal(raw, &items); e != nil {
			return nil, e
		}
	}
	out := []json.RawMessage{}
	for _, item := range items {
		units, e := protocol.DecodeJavaScriptJSONString(item)
		if e != nil {
			return nil, e
		}
		current := []uint16{}
		parens := false
		push := func() {
			start, end := 0, len(current)
			for start < end && invocationWhitespace(current[start]) {
				start++
			}
			for start < end && invocationWhitespace(current[end-1]) {
				end--
			}
			if end > start {
				out = append(out, protocol.QuoteJavaScriptUTF16(current[start:end]))
			}
			current = nil
		}
		for _, unit := range units {
			switch unit {
			case '(':
				parens = true
				current = append(current, unit)
			case ')':
				parens = false
				current = append(current, unit)
			case ',':
				if parens {
					current = append(current, unit)
				} else {
					push()
				}
			case ' ':
				if parens {
					current = append(current, unit)
				} else {
					push()
				}
			default:
				current = append(current, unit)
			}
		}
		push()
	}
	return out, nil
}
func invocationWhitespace(unit uint16) bool {
	return unit >= 9 && unit <= 13 || unit == ' ' || unit == 0xa0 || unit == 0x1680 || unit >= 0x2000 && unit <= 0x200a || unit == 0x2028 || unit == 0x2029 || unit == 0x202f || unit == 0x205f || unit == 0x3000 || unit == 0xfeff
}
