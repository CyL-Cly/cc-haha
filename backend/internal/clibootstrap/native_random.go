package clibootstrap

import (
	"crypto/rand"
	"encoding/binary"
)

// nativeMathRandom supplies the native random primitive to NotebookEdit. The
// tool retains Source's radix conversion and cell-ID construction.
func nativeMathRandom() float64 {
	var bits [8]byte
	if _, err := rand.Read(bits[:]); err != nil {
		panic(err)
	}
	return float64(binary.BigEndian.Uint64(bits[:])>>11) / (1 << 53)
}
