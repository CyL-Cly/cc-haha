package protocol

import "strings"

// DecodeJavaScriptUTF8 matches Node UTF-8 reads and WHATWG replacement decoding.
// It retains a BOM; TextDecoder callers separately remove one leading BOM from
// each decoded input. An invalid sequence consumes its accepted prefix once,
// while each invalid lead byte produces a separate replacement character.
func DecodeJavaScriptUTF8(data []byte) []byte {
	var output strings.Builder
	for index := 0; index < len(data); {
		lead := data[index]
		if lead < 0x80 {
			output.WriteByte(lead)
			index++
			continue
		}
		size, lower, upper := 0, byte(0x80), byte(0xbf)
		switch {
		case lead >= 0xc2 && lead <= 0xdf:
			size = 2
		case lead >= 0xe0 && lead <= 0xef:
			size = 3
			if lead == 0xe0 {
				lower = 0xa0
			}
			if lead == 0xed {
				upper = 0x9f
			}
		case lead >= 0xf0 && lead <= 0xf4:
			size = 4
			if lead == 0xf0 {
				lower = 0x90
			}
			if lead == 0xf4 {
				upper = 0x8f
			}
		}
		consumed := 1
		for consumed < size && index+consumed < len(data) {
			next := data[index+consumed]
			if next < lower || next > upper {
				break
			}
			consumed++
			lower, upper = 0x80, 0xbf
		}
		if size != 0 && consumed == size {
			output.Write(data[index : index+size])
		} else {
			output.WriteRune('\ufffd')
		}
		index += consumed
	}
	return []byte(output.String())
}
