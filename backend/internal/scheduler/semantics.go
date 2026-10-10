package scheduler

import (
	"bytes"
	"encoding/json"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var integerPrefix = regexp.MustCompile(`^[+-]?[0-9]+`)

func parseIntegerPrefix(value string) float64 {
	prefix := integerPrefix.FindString(jsTrim(value))
	if prefix == "" {
		return math.NaN()
	}
	number, e := strconv.ParseFloat(prefix, 64)
	if e != nil && number == 0 {
		return math.NaN()
	}
	return number
}
func jsNumberString(value string) float64 {
	value = jsTrim(value)
	if value == "" {
		return 0
	}
	if value == "Infinity" || value == "+Infinity" {
		return math.Inf(1)
	}
	if value == "-Infinity" {
		return math.Inf(-1)
	}
	if len(value) > 2 && value[0] == '0' {
		base := 0
		switch value[1] {
		case 'x', 'X':
			base = 16
		case 'b', 'B':
			base = 2
		case 'o', 'O':
			base = 8
		}
		if base != 0 {
			number, ok := new(big.Int).SetString(value[2:], base)
			if !ok {
				return math.NaN()
			}
			out, _ := new(big.Float).SetInt(number).Float64()
			return out
		}
	}
	number, e := strconv.ParseFloat(value, 64)
	if e != nil && !math.IsInf(number, 0) {
		return math.NaN()
	}
	return number
}
func FieldMatches(field string, value int) bool {
	if field == "*" {
		return true
	}
	v := float64(value)
	for _, part := range strings.Split(field, ",") {
		part = jsTrim(part)
		if strings.Contains(part, "/") {
			pieces := strings.Split(part, "/")
			step := parseIntegerPrefix(pieces[1])
			if math.IsNaN(step) || step <= 0 {
				continue
			}
			start := 0.0
			if pieces[0] == "*" {
				if math.Mod(v, step) == 0 {
					return true
				}
				continue
			}
			if strings.Contains(pieces[0], "-") {
				bounds := strings.Split(pieces[0], "-")
				start, end := parseIntegerPrefix(bounds[0]), parseIntegerPrefix(bounds[1])
				if v < start || v > end {
					continue
				}
				if math.Mod(v-start, step) == 0 {
					return true
				}
				continue
			}
			start = parseIntegerPrefix(pieces[0])
			if v >= start && math.Mod(v-start, step) == 0 {
				return true
			}
			continue
		}
		if strings.Contains(part, "-") {
			bounds := strings.Split(part, "-")
			if v >= parseIntegerPrefix(bounds[0]) && v <= parseIntegerPrefix(bounds[1]) {
				return true
			}
			continue
		}
		if parseIntegerPrefix(part) == v {
			return true
		}
	}
	return false
}
func CronMatches(expression string, date time.Time) bool {
	fields := strings.FieldsFunc(jsTrim(expression), func(r rune) bool { return jsTrim(string(r)) == "" })
	if len(fields) != 5 {
		return false
	}
	values := []int{date.Minute(), date.Hour(), date.Day(), int(date.Month()), int(date.Weekday())}
	for i, field := range fields {
		if !FieldMatches(field, values[i]) {
			return false
		}
	}
	return true
}
func ResolveTaskTimeoutMs(environment map[string]string) float64 {
	value := jsTrim(environment["CC_HAHA_TASK_TIMEOUT_MS"])
	if value == "" {
		return 600000
	}
	number := jsNumberString(value)
	if !math.IsNaN(number) && !math.IsInf(number, 0) && math.Trunc(number) == number && number > 0 {
		return number
	}
	return 600000
}
func timerDuration(milliseconds float64) time.Duration {
	if milliseconds > 2147483647 || milliseconds < 1 || math.IsNaN(milliseconds) {
		milliseconds = 1
	}
	return time.Duration(math.Trunc(milliseconds)) * time.Millisecond
}
func trimUnits(units []uint16) []uint16 {
	space := func(unit uint16) bool { return jsTrim(string(rune(unit))) == "" }
	start, end := 0, len(units)
	for start < end && space(units[start]) {
		start++
	}
	for end > start && space(units[end-1]) {
		end--
	}
	return units[start:end]
}
func truncateJSONText(value json.RawMessage, n int) json.RawMessage {
	units, e := protocol.DecodeJavaScriptJSONString(value)
	if e != nil {
		return quoted("")
	}
	if len(units) > n {
		units = units[:n]
	}
	return protocol.QuoteJavaScriptUTF16(units)
}
func extractAssistantText(rawOutput string) json.RawMessage {
	parts := [][]uint16{}
	appendText := func(raw json.RawMessage, dedup bool) {
		units, e := protocol.DecodeJavaScriptJSONString(raw)
		if e != nil {
			return
		}
		units = trimUnits(units)
		if len(units) == 0 {
			return
		}
		if dedup && len(parts) > 0 {
			last := protocol.QuoteJavaScriptUTF16(parts[len(parts)-1])
			if bytes.Equal(last, protocol.QuoteJavaScriptUTF16(units)) {
				return
			}
			joined := []uint16{}
			for i, part := range parts {
				if i > 0 {
					joined = append(joined, '\n', '\n')
				}
				joined = append(joined, part...)
			}
			if bytes.Equal(protocol.QuoteJavaScriptUTF16(joined), protocol.QuoteJavaScriptUTF16(units)) {
				return
			}
		}
		parts = append(parts, append([]uint16{}, units...))
	}
	for _, line := range strings.Split(rawOutput, "\n") {
		var envelope protocol.JSONObject
		if envelope.UnmarshalJSON([]byte(line)) != nil || !envelope.IsObject() {
			continue
		}
		if envelope.String("type") == "assistant" {
			var message protocol.JSONObject
			_ = message.UnmarshalJSON(envelope.Get("message"))
			var blocks []protocol.JSONObject
			_ = json.Unmarshal(message.Get("content"), &blocks)
			for _, block := range blocks {
				if block.String("type") == "text" {
					appendText(block.Get("text"), false)
				}
			}
		}
		if envelope.String("type") == "result" {
			value := envelope.Get("result")
			if len(value) > 0 && value[0] == '"' {
				appendText(value, true)
			} else {
				var result protocol.JSONObject
				_ = result.UnmarshalJSON(value)
				appendText(result.Get("message"), true)
			}
		}
	}
	joined := []uint16{}
	for i, part := range parts {
		if i > 0 {
			joined = append(joined, '\n', '\n')
		}
		joined = append(joined, part...)
	}
	return protocol.QuoteJavaScriptUTF16(joined)
}
func ExtractAssistantText(output string) string {
	units, _ := protocol.DecodeJavaScriptJSONString(extractAssistantText(output))
	return string(utf16.Decode(units))
}
