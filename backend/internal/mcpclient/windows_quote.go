package mcpclient

import "strings"

// Windows cmd escaping follows the cross-spawn implementation used by the
// original SDK, including the second escape pass for npm .bin command shims.
func escapeWindowsMeta(value string) string {
	var b strings.Builder
	for _, r := range value {
		if strings.ContainsRune("()[]%!^\"`<>&|;, *?", r) {
			b.WriteByte('^')
		}
		b.WriteRune(r)
	}
	return b.String()
}
func escapeWindowsArgument(value string, double bool) string {
	var b strings.Builder
	slashes := 0
	for _, r := range value {
		if r == '\\' {
			slashes++
			continue
		}
		if r == '"' {
			b.WriteString(strings.Repeat("\\", slashes*2+1))
			b.WriteRune(r)
		} else {
			b.WriteString(strings.Repeat("\\", slashes))
			b.WriteRune(r)
		}
		slashes = 0
	}
	b.WriteString(strings.Repeat("\\", slashes*2))
	s := escapeWindowsMeta("\"" + b.String() + "\"")
	if double {
		s = escapeWindowsMeta(s)
	}
	return s
}
