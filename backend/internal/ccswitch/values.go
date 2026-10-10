// Package ccswitch reads the original local cc-switch stores without changing
// them. Credentials remain in server-side Entry values, never the scan payload.
package ccswitch

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func raw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return protocol.StringifyValidatedJavaScriptJSON(b)
}
func obj(v json.RawMessage) protocol.JSONObject {
	var o protocol.JSONObject
	_ = json.Unmarshal(v, &o)
	return o
}
func field(k string, v any) protocol.JSONField { return protocol.JSONField{Name: k, Value: raw(v)} }
func record(fields ...protocol.JSONField) protocol.JSONObject {
	return protocol.NewJSONObject(fields...)
}
func array(v json.RawMessage) []json.RawMessage {
	var out []json.RawMessage
	_ = json.Unmarshal(v, &out)
	return out
}
func isString(v json.RawMessage) bool {
	_, e := protocol.DecodeJavaScriptJSONString(v)
	return e == nil
}
func units(v json.RawMessage) []uint16 { u, _ := protocol.DecodeJavaScriptJSONString(v); return u }
func sameString(a, b json.RawMessage) bool {
	if !isString(a) || !isString(b) {
		return false
	}
	return string(protocol.QuoteJavaScriptUTF16(units(a))) == string(protocol.QuoteJavaScriptUTF16(units(b)))
}
func trimmed(v json.RawMessage) json.RawMessage {
	u, e := protocol.DecodeJavaScriptJSONString(v)
	if e != nil {
		return nil
	}
	for len(u) > 0 && protocol.IsJavaScriptWhitespace(rune(u[0])) {
		u = u[1:]
	}
	for len(u) > 0 && protocol.IsJavaScriptWhitespace(rune(u[len(u)-1])) {
		u = u[:len(u)-1]
	}
	if len(u) == 0 {
		return nil
	}
	return protocol.QuoteJavaScriptUTF16(u)
}
func text(v json.RawMessage) string  { return string(utf16.Decode(units(v))) }
func str(s string) json.RawMessage   { return raw(s) }
func lower(v json.RawMessage) string { return strings.ToLower(text(trimmed(v))) }
func parseJSONLike(v json.RawMessage) protocol.JSONObject {
	if o := obj(v); o.IsObject() {
		return o
	}
	if isString(v) {
		return obj([]byte(protocol.TrimJavaScriptString(text(v))))
	}
	return protocol.JSONObject{}
}
func number(v json.RawMessage, blankIsZero bool) *float64 {
	if len(v) == 0 || string(v) == "null" || string(v) == "true" || string(v) == "false" {
		return nil
	}
	s := string(v)
	if isString(v) {
		s = protocol.TrimJavaScriptString(text(v))
		if s == "" {
			if blankIsZero {
				n := float64(0)
				return &n
			}
			return nil
		}
	}
	var n float64
	var err error
	if len(s) > 2 && s[0] == '0' && strings.ContainsRune("xXbBoO", rune(s[1])) {
		base := map[byte]int{'x': 16, 'X': 16, 'b': 2, 'B': 2, 'o': 8, 'O': 8}[s[1]]
		var value uint64
		value, err = strconv.ParseUint(s[2:], base, 64)
		n = float64(value)
	} else {
		n, err = strconv.ParseFloat(s, 64)
	}
	if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
		return nil
	}
	return &n
}
func contextWindow(v json.RawMessage) *float64 {
	n := number(v, false)
	if n == nil || math.Trunc(*n) != *n || *n < 16000 || *n > 10000000 {
		return nil
	}
	return n
}
func boolean(v json.RawMessage) bool {
	return string(v) == "true" || string(v) == "1" || string(v) == `"1"` || string(v) == `"true"`
}
func less(a, b json.RawMessage) bool {
	x, y := units(a), units(b)
	for i := 0; i < len(x) && i < len(y); i++ {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return len(x) < len(y)
}
func appendText(values ...json.RawMessage) json.RawMessage {
	v, _ := protocol.JoinJavaScriptValuesJSON(values, "")
	return v
}
func boolEnv(v json.RawMessage, toolSearch bool) bool {
	s := lower(v)
	if s == "1" || s == "true" || s == "on" || s == "yes" {
		return true
	}
	return toolSearch && (s == "auto" || strings.HasPrefix(s, "auto:"))
}
