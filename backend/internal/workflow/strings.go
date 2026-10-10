package workflow

import "strings"

func javascriptSpace(r rune) bool {
	switch r {
	case 0x9, 0xa, 0xb, 0xc, 0xd, 0x20, 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}
func javascriptTrim(value string) string { return strings.TrimFunc(value, javascriptSpace) }
