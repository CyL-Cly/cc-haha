package projectpath

import (
	"encoding/binary"
	"math/bits"
)

// One-shot translation of lib/std/hash/wyhash.zig at oven-sh/zig
// 04e7f6ac1e009525bc00934f20199c68f04e0a24. This is Bun v1.3.14's
// default build toolchain; the actual pinned Bun executable is tested separately.
func wyhash(seed uint64, input []byte) uint64 {
	secret := [4]uint64{0xa0761d6478bd642f, 0xe7037ed1a0b428db, 0x8ebc6af09c88c6e3, 0x589965cc75374cc3}
	mix := func(a, b uint64) uint64 {
		hi, lo := bits.Mul64(a, b)
		return lo ^ hi
	}
	state := [3]uint64{}
	state[0] = seed ^ mix(seed^secret[0], secret[1])
	state[1], state[2] = state[0], state[0]
	var a, b uint64
	length := len(input)
	if length <= 16 {
		if length >= 4 {
			end := length - 4
			quarter := (length >> 3) << 2
			a = uint64(binary.LittleEndian.Uint32(input[:4]))<<32 | uint64(binary.LittleEndian.Uint32(input[quarter:quarter+4]))
			b = uint64(binary.LittleEndian.Uint32(input[end:end+4]))<<32 | uint64(binary.LittleEndian.Uint32(input[end-quarter:end-quarter+4]))
		} else if length > 0 {
			a = uint64(input[0])<<16 | uint64(input[length>>1])<<8 | uint64(input[length-1])
		}
	} else {
		i := 0
		if length >= 48 {
			for i+48 < length {
				for j := 0; j < 3; j++ {
					x := binary.LittleEndian.Uint64(input[i+16*j : i+16*j+8])
					y := binary.LittleEndian.Uint64(input[i+16*j+8 : i+16*j+16])
					state[j] = mix(x^secret[j+1], y^state[j])
				}
				i += 48
			}
			state[0] ^= state[1] ^ state[2]
		}
		for i+16 < length {
			state[0] = mix(binary.LittleEndian.Uint64(input[i:i+8])^secret[1], binary.LittleEndian.Uint64(input[i+8:i+16])^state[0])
			i += 16
		}
		a = binary.LittleEndian.Uint64(input[length-16 : length-8])
		b = binary.LittleEndian.Uint64(input[length-8:])
	}
	a ^= secret[1]
	b ^= state[0]
	hi, lo := bits.Mul64(a, b)
	return mix(lo^secret[0]^uint64(length), hi^secret[1])
}
