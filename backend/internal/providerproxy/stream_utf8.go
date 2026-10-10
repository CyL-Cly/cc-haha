package providerproxy

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

func responseUTF8Prefix(data []byte) int {
	for index := 0; index < len(data); {
		lead := data[index]
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
		if size == 0 {
			index++
			continue
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
		if consumed < size && index+consumed == len(data) {
			return index
		}
		index += consumed
	}
	return len(data)
}
func (p *ResponsesProcessor) decode(chunk []byte, final bool) string {
	data := append(append([]byte(nil), p.utf8Pending...), chunk...)
	size := len(data)
	if !final {
		size = responseUTF8Prefix(data)
	}
	p.utf8Pending = append([]byte(nil), data[size:]...)
	decoded := protocol.DecodeJavaScriptUTF8(data[:size])
	if !p.decoderStarted && len(decoded) > 0 {
		p.decoderStarted = true
		if len(decoded) >= 3 && decoded[0] == 0xef && decoded[1] == 0xbb && decoded[2] == 0xbf {
			decoded = decoded[3:]
		}
	}
	return string(decoded)
}
