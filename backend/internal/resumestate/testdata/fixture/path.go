package main

import "unicode/utf16"

// Source's short project directory spelling is observed from original HOME,
// not from a selected Source result. Long hashes remain a rejected capability.
func sanitized(path string) string {
	result := []byte{}
	for _, unit := range utf16.Encode([]rune(path)) {
		if unit >= 'a' && unit <= 'z' || unit >= 'A' && unit <= 'Z' || unit >= '0' && unit <= '9' {
			result = append(result, byte(unit))
		} else {
			result = append(result, '-')
		}
	}
	return string(result)
}
