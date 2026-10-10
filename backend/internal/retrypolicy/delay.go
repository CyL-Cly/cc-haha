// Package retrypolicy preserves retry decisions and attempt scheduling for the
// shared query host. Request construction and stream consumption stay separate.
package retrypolicy

import (
	"math"
	"strconv"
	"strings"
)

const BaseDelayMillis = 500

// Delay matches getRetryDelay in the Source retry host. The caller supplies one
// random sample so the real host can use its RNG and replay can freeze it. A
// numeric Retry-After directive bypasses both the cap and the random sample.
func Delay(attempt float64, retryAfter *string, maximum float64, random func() float64) float64 {
	if retryAfter != nil && *retryAfter != "" {
		seconds := decimalPrefix(*retryAfter)
		if !math.IsNaN(seconds) && !math.IsInf(seconds, 0) {
			return math.Max(BaseDelayMillis, seconds*1000)
		}
	}
	base := BaseDelayMillis * math.Pow(2, attempt-1)
	if math.IsNaN(base) || math.IsNaN(maximum) {
		base = math.NaN() // JS Math.min propagates NaN even alongside -Infinity.
	} else {
		base = math.Min(base, maximum)
	}
	return base + random()*0.25*base
}

// DefaultMaximum preserves the Source's environment parseInt result, including
// negative zero, negative values, NaN and overflow. The attempt host, rather
// than this parser, owns the resulting iteration and terminal error behavior.
func DefaultMaximum(environmentValue string) float64 {
	if environmentValue == "" {
		return 10
	}
	return decimalPrefix(environmentValue)
}

func StreamTransientMaximum(environmentValue string) float64 {
	value := decimalPrefix(environmentValue)
	if !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 {
		return math.Min(value, 5)
	}
	return 2
}

// parseInt(text, 10) trims ECMAScript whitespace and consumes only a decimal
// prefix. Parsing via float64 preserves the same rounding and overflow as JS.
func decimalPrefix(value string) float64 {
	value = strings.TrimLeftFunc(value, jsWhitespace)
	end := 0
	if value != "" && (value[0] == '+' || value[0] == '-') {
		end++
	}
	start := end
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if end == start {
		return math.NaN()
	}
	parsed, _ := strconv.ParseFloat(value[:end], 64)
	return parsed
}

func jsWhitespace(r rune) bool {
	return r >= '\t' && r <= '\r' || r == ' ' || r == '\u00a0' || r == '\u1680' ||
		r >= '\u2000' && r <= '\u200a' || r == '\u2028' || r == '\u2029' ||
		r == '\u202f' || r == '\u205f' || r == '\u3000' || r == '\ufeff'
}
