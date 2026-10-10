package builtintools

import "strings"

func skillSimple(value string) bool {
	switch strings.ToLower(strings.TrimFunc(value, func(r rune) bool {
		return r >= 9 && r <= 13 || r == ' ' || r == '\u00a0' || r == '\u1680' || r >= '\u2000' && r <= '\u200a' || r == '\u2028' || r == '\u2029' || r == '\u202f' || r == '\u205f' || r == '\u3000' || r == '\ufeff'
	})) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
