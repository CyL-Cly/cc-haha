package protocol

import "strings"

func IsJavaScriptWhitespace(r rune) bool {
	return r >= 9 && r <= 13 || r == 0x20 || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
}
func TrimJavaScriptString(s string) string { return strings.TrimFunc(s, IsJavaScriptWhitespace) }
