package windowshell

import (
	"math"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func positiveEnvInteger(value string, fallback float64) float64 {
	value = protocol.TrimJavaScriptString(value)
	if strings.HasPrefix(value, "+") {
		value = value[1:]
	}
	end := 0
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if end == 0 {
		return fallback
	}
	number, _ := strconv.ParseFloat(value[:end], 64)
	if number > 0 {
		return number
	}
	return fallback
}

func DefaultTimeout(env map[string]string) float64 {
	return positiveEnvInteger(env["BASH_DEFAULT_TIMEOUT_MS"], 120000)
}
func MaxTimeout(env map[string]string) float64 {
	return math.Max(DefaultTimeout(env), positiveEnvInteger(env["BASH_MAX_TIMEOUT_MS"], 600000))
}
func OutputLimit(env map[string]string) int {
	return int(math.Min(positiveEnvInteger(env["BASH_MAX_OUTPUT_LENGTH"], 30000), 150000))
}
func NumberString(value float64) string {
	if math.IsInf(value, 1) {
		return "Infinity"
	}
	if math.IsInf(value, -1) {
		return "-Infinity"
	}
	if math.IsNaN(value) {
		return "NaN"
	}
	if value == 0 {
		return "0"
	}
	if math.Abs(value) >= 1e-6 && math.Abs(value) < 1e21 {
		return strconv.FormatFloat(value, 'f', -1, 64)
	}
	text := strconv.FormatFloat(value, 'e', -1, 64)
	text = strings.Replace(text, "e-0", "e-", 1)
	return strings.Replace(text, "e+0", "e+", 1)
}
