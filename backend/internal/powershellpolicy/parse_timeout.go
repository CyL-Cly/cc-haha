package powershellpolicy

import (
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
)

// ParseTimeoutMilliseconds mirrors Source parseInt(env, 10), including JS
// whitespace, a leading sign, a consumed decimal prefix and Number rounding.
func ParseTimeoutMilliseconds(value string) float64 {
	value = jsTrim(value)
	if strings.HasPrefix(value, "+") {
		value = value[1:]
	}
	end := 0
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if end == 0 {
		return 5000
	}
	number, _ := strconv.ParseFloat(value[:end], 64)
	if number > 0 {
		return number
	}
	return 5000
}
func parseTimeoutNumber(value float64) string { return windowshell.NumberString(value) }
