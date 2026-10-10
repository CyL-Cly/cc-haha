package querypreflight

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

var integerPrefix = regexp.MustCompile(`^[+-]?[0-9]+`)
var floatPrefix = regexp.MustCompile(`^[+-]?(?:Infinity|(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?)`)

func trimmed(v string) string {
	return strings.TrimFunc(v, func(r rune) bool {
		return r == '\ufeff' || strings.ContainsRune(" \t\n\r\v\f\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000", r)
	})
}
func parsePrefix(v string, re *regexp.Regexp) float64 {
	part := re.FindString(trimmed(v))
	if part == "" {
		return math.NaN()
	}
	// The pinned prefix regex accepts only decimal or Infinity. Range overflow
	// is JS Infinity, not an invalid prefix; syntax errors cannot reach here.
	n, _ := strconv.ParseFloat(part, 64)
	return n
}
func parseInteger(v string) float64 { return parsePrefix(v, integerPrefix) }
func thresholds(env map[string]string) (float64, float64) {
	window := float64(200000)
	if parsed := parseInteger(env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]); parsed > 0 {
		window = parsed
	}
	// Both declared custom models use original default max output 32000.
	reserve := math.Min(32000, math.Min(20000, math.Floor(window*.25)))
	effective := window - reserve
	threshold := effective - math.Min(13000, math.Floor(effective/3))
	pct := parsePrefix(env["CLAUDE_AUTOCOMPACT_PCT_OVERRIDE"], floatPrefix)
	if pct > 0 && pct <= 100 {
		threshold = math.Min(threshold, math.Floor(effective*(pct/100)))
	}
	return effective, threshold
}
