package queryloop

import (
	"math"
	"strconv"
	"strings"
)

// SourceToolUseConcurrency consumes toolOrchestration's decimal parseInt || 10
// control. Values beyond Go's index range need only admit the finite input
// slice, so saturation retains generators.all's observable admission limit.
func SourceToolUseConcurrency(raw string) int {
	raw = strings.TrimLeftFunc(raw, func(r rune) bool {
		return r >= 0x9 && r <= 0xd || r == 0x20 || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
	})
	length := 0
	if len(raw) > 0 && (raw[0] == '+' || raw[0] == '-') {
		length++
	}
	first := length
	for length < len(raw) && raw[length] >= '0' && raw[length] <= '9' {
		length++
	}
	if first == length {
		return 10
	}
	parsed, _ := strconv.ParseFloat(raw[:length], 64)
	if parsed == 0 || math.IsNaN(parsed) {
		return 10
	}
	maximum := int(^uint(0) >> 1)
	if parsed >= float64(maximum) {
		return maximum
	}
	if parsed <= -float64(maximum) {
		return -maximum
	}
	return int(parsed)
}
