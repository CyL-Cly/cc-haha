//go:build !darwin

package textsanitization

import "unicode/utf16"

func platformNFKC(units []uint16, table *unicodeTables) ([]rune, error) {
	return table.nfkc(utf16.Decode(units)), nil
}
