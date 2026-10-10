package goalstate

import (
	"encoding/json"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func ContinuationOutput(reason string) string {
	units, _ := protocol.DecodeJavaScriptJSONString(ContinuationOutputJSON(reason))
	return string(utf16.Decode(units))
}

func ContinuationOutputJSON(reason string) []byte {
	raw, _ := json.Marshal(reason)
	return ContinuationOutputRawJSON(raw)
}

// ContinuationOutputRawJSON retains both existing lone surrogates and Source's
// 240 UTF-16 code-unit slice. XML-sensitive characters become spaces.
func ContinuationOutputRawJSON(raw json.RawMessage) json.RawMessage {
	units, _ := protocol.DecodeJavaScriptJSONString(raw)
	prefix := []byte("Prompt hook condition was not met:")
	matched := len(units) >= len(prefix)
	for i, c := range prefix {
		if !matched {
			break
		}
		u := units[i]
		if u >= 'A' && u <= 'Z' {
			u += 'a' - 'A'
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if u != uint16(c) {
			matched = false
		}
	}
	if matched {
		units = units[len(prefix):]
	}
	normalized := []uint16{}
	space := false
	for _, u := range units {
		if u == '<' || u == '>' || u == '&' || jsSpace(rune(u)) {
			if len(normalized) > 0 {
				space = true
			}
			continue
		}
		if space {
			normalized = append(normalized, ' ')
			space = false
		}
		normalized = append(normalized, u)
	}
	if len(normalized) == 0 {
		return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune("Goal continuing: more work is required")))
	}
	if len(normalized) > 240 {
		normalized = normalized[:240]
	}
	return protocol.QuoteJavaScriptUTF16(append(utf16.Encode([]rune("Goal continuing: ")), normalized...))
}
