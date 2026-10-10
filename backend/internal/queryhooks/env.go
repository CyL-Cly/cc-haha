package queryhooks

// isEnvTruthy lowercases then ECMAScript String.trim()s. Go's TrimSpace admits
// U+0085 and omits U+FEFF, so it does not define this Source boundary.
func javaScriptWhitespace(value rune) bool {
	switch value {
	case '\t', '\n', '\v', '\f', '\r', ' ', '\u00a0', '\u1680', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000', '\ufeff':
		return true
	}
	return value >= '\u2000' && value <= '\u200a'
}
