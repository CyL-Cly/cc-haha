package queryhooks

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"math"
	"strconv"
	"strings"
	"time"
)

// StructuredIO's timeout is read by executeHooksOutsideREPL with JavaScript's
// truthiness and multiplication, then passed to Bun's timer primitive.
func sdkCallbackTimeout(raw json.RawMessage, fallback time.Duration) (time.Duration, error) {
	ms := float64(fallback / time.Millisecond)
	truthy := hookTruthy(raw)
	if value := bytes.TrimSpace(raw); len(value) > 0 && value[0] != '"' && value[0] != '[' && value[0] != '{' && value[0] != 't' && value[0] != 'f' && value[0] != 'n' {
		n, e := strconv.ParseFloat(string(value), 64)
		truthy = (e == nil || math.IsInf(n, 0)) && n != 0 && !math.IsNaN(n)
	}
	if truthy {
		value := bytes.TrimSpace(raw)
		switch string(value) {
		case "true":
			ms = 1000
		case "false", "null":
			ms = 0
		default:
			text := string(value)
			if len(value) > 0 && (value[0] == '"' || value[0] == '[' || value[0] == '{') {
				converted, e := protocol.ExactJavaScriptStringJSON(value)
				if e != nil {
					return 0, e
				}
				if e = json.Unmarshal(converted, &text); e != nil {
					return 0, e
				}
			}
			text = protocol.TrimJavaScriptString(text)
			n := 0.0
			if text != "" {
				var e error
				if len(text) > 2 && text[0] == '0' && strings.ContainsAny(text[1:2], "xXbBoO") {
					base := 16
					switch text[1] {
					case 'b', 'B':
						base = 2
					case 'o', 'O':
						base = 8
					}
					var integer uint64
					integer, e = strconv.ParseUint(text[2:], base, 64)
					n = float64(integer)
				} else {
					n, e = strconv.ParseFloat(text, 64)
				}
				if e != nil && !math.IsInf(n, 0) {
					n = math.NaN()
				}
			}
			ms = n * 1000
		}
	}
	// The actual pinned Bun timer accepts zero/negative/NaN/infinity/overflow
	// with a minimum timer tick. It truncates fractional milliseconds.
	if math.IsNaN(ms) || math.IsInf(ms, 0) || ms < 1 || ms > 2147483647 {
		ms = 1
	}
	return time.Duration(math.Trunc(ms)) * time.Millisecond, nil
}
