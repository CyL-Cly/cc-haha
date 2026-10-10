package pluginzip

import "errors"

// Raw DEFLATE decoding follows the original fflate extraction semantics rather
// than archive/zip's CRC and size checks. In particular, the original ignores
// CRC/NLEN and uses a fixed output buffer sized from the central directory.

type bits struct {
	data     []byte
	position int
}

func (b *bits) read(n int) uint32 {
	var value uint32
	for i := 0; i < n; i++ {
		at := b.position + i
		if at/8 < len(b.data) {
			value |= uint32((b.data[at/8]>>uint(at%8))&1) << uint(i)
		}
	}
	b.position += n
	return value
}
func (b *bits) over() bool { return b.position > len(b.data)*8 }

type huffman struct {
	values map[uint32]int
	max    int
}

func treeFrom(lengths []int) huffman {
	counts := [16]int{}
	max := 0
	for _, length := range lengths {
		if length > 0 && length <= 15 {
			counts[length]++
			if length > max {
				max = length
			}
		}
	}
	next := [16]int{}
	code := 0
	for width := 1; width <= 15; width++ {
		code = (code + counts[width-1]) << 1
		next[width] = code
	}
	t := huffman{values: map[uint32]int{}, max: max}
	for symbol, length := range lengths {
		if length > 0 && length <= 15 {
			t.values[uint32(length)<<16|uint32(next[length])] = symbol
			next[length]++
		}
	}
	return t
}
func (t huffman) decode(b *bits) (int, bool) {
	code := uint32(0)
	for width := 1; width <= t.max; width++ {
		code = code<<1 | b.read(1)
		if symbol, ok := t.values[uint32(width)<<16|code]; ok {
			return symbol, true
		}
	}
	return 0, false
}
func inflateError(code int) error {
	messages := []string{"unexpected EOF", "invalid block type", "invalid length/literal", "invalid distance"}
	return &ZipError{Code: code, Message: messages[code]}
}

var lengthBase = []int{3, 4, 5, 6, 7, 8, 9, 10, 11, 13, 15, 17, 19, 23, 27, 31, 35, 43, 51, 59, 67, 83, 99, 115, 131, 163, 195, 227, 258}
var lengthExtra = []int{0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0}
var distanceBase = []int{1, 2, 3, 4, 5, 7, 9, 13, 17, 25, 33, 49, 65, 97, 129, 193, 257, 385, 513, 769, 1025, 1537, 2049, 3073, 4097, 6145, 8193, 12289, 16385, 24577}
var distanceExtra = []int{0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13}

func inflateRaw(data []byte, size int) ([]byte, error) {
	output := make([]byte, size)
	if len(data) == 0 {
		return output, nil
	}
	b := bits{data: data}
	written := 0
	put := func(value byte) {
		if written < len(output) {
			output[written] = value
		}
		written++
	}
	for {
		final, kind := b.read(1) != 0, b.read(2)
		if kind == 0 {
			b.position = (b.position + 7) &^ 7
			length := int(b.read(16))
			_ = b.read(16)
			start := b.position / 8
			if start+length > len(data) {
				return nil, inflateError(0)
			}
			if written+length > len(output) {
				return nil, errors.New("offset is out of bounds")
			}
			copy(output[written:], data[start:start+length])
			written += length
			b.position = (start + length) * 8
			if final {
				break
			}
			continue
		}
		if kind == 3 {
			return nil, inflateError(1)
		}
		var literal, distance huffman
		if kind == 1 {
			lengths := make([]int, 288)
			for i := range lengths {
				switch {
				case i < 144:
					lengths[i] = 8
				case i < 256:
					lengths[i] = 9
				case i < 280:
					lengths[i] = 7
				default:
					lengths[i] = 8
				}
			}
			literal = treeFrom(lengths)
			lengths = make([]int, 32)
			for i := range lengths {
				lengths[i] = 5
			}
			distance = treeFrom(lengths)
		} else {
			litCount, distCount, codeCount := int(b.read(5))+257, int(b.read(5))+1, int(b.read(4))+4
			codeLengths := make([]int, 19)
			order := []int{16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15}
			for i := 0; i < codeCount; i++ {
				codeLengths[order[i]] = int(b.read(3))
			}
			codeTree := treeFrom(codeLengths)
			lengths := make([]int, litCount+distCount)
			for at := 0; at < len(lengths); {
				symbol, ok := codeTree.decode(&b)
				if !ok {
					if b.over() {
						return nil, inflateError(0)
					}
					return nil, inflateError(2)
				}
				if symbol < 16 {
					lengths[at] = symbol
					at++
					continue
				}
				repeat, value := 0, 0
				switch symbol {
				case 16:
					repeat = 3 + int(b.read(2))
					if at > 0 {
						value = lengths[at-1]
					}
				case 17:
					repeat = 3 + int(b.read(3))
				case 18:
					repeat = 11 + int(b.read(7))
				default:
					return nil, inflateError(2)
				}
				for i := 0; i < repeat; i++ {
					if at < len(lengths) {
						lengths[at] = value
					}
					at++
				}
			}
			literal, distance = treeFrom(lengths[:litCount]), treeFrom(lengths[litCount:])
		}
		if b.over() {
			return nil, inflateError(0)
		}
		for {
			symbol, ok := literal.decode(&b)
			if b.over() {
				return nil, inflateError(0)
			}
			if !ok {
				return nil, inflateError(2)
			}
			if symbol < 256 {
				put(byte(symbol))
				continue
			}
			if symbol == 256 {
				break
			}
			index := symbol - 257
			if index < 0 || index >= len(lengthBase) {
				return nil, inflateError(2)
			}
			length := lengthBase[index] + int(b.read(lengthExtra[index]))
			d, ok := distance.decode(&b)
			if !ok || d >= len(distanceBase) {
				return nil, inflateError(3)
			}
			offset := distanceBase[d] + int(b.read(distanceExtra[d]))
			if b.over() {
				return nil, inflateError(0)
			}
			if offset > written {
				return nil, inflateError(3)
			}
			for i := 0; i < length; i++ {
				value := byte(0)
				source := written - offset
				if source >= 0 && source < len(output) {
					value = output[source]
				}
				put(value)
			}
		}
		if final {
			break
		}
	}
	if written < len(output) {
		return output[:written], nil
	}
	return output, nil
}
