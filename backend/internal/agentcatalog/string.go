package agentcatalog

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
	"unicode/utf16"
)

func trimJS(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return r >= '\t' && r <= '\r' || r == ' ' || r == '\u00a0' || r == '\u1680' || r >= '\u2000' && r <= '\u200a' || r == '\u2028' || r == '\u2029' || r == '\u202f' || r == '\u205f' || r == '\u3000' || r == '\ufeff'
	})
}
func exactString(raw json.RawMessage) (string, error) {
	units, e := protocol.DecodeJavaScriptJSONString(raw)
	if e != nil {
		return "", nil
	}
	for i := 0; i < len(units); i++ {
		u := units[i]
		if u >= 0xd800 && u <= 0xdbff {
			if i+1 == len(units) || units[i+1] < 0xdc00 || units[i+1] > 0xdfff {
				return "", cap("lone UTF16 listing string")
			}
			i++
		} else if u >= 0xdc00 && u <= 0xdfff {
			return "", cap("lone UTF16 listing string")
		}
	}
	return string(utf16.Decode(units)), nil
}
