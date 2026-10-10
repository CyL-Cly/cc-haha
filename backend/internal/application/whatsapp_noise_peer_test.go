package application

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"go.mau.fi/libsignal/kdf"
)

// Private peer owns the server half of the same Noise XX transcript; the
// product client still performs real encryption and certificate verification.
type noisePeer struct {
	hash, salt []byte
	key        cipher.AEAD
	counter    uint32
}

func newNoisePeer(pattern string, header []byte) *noisePeer {
	hash := []byte(pattern)
	if len(hash) != 32 {
		sum := sha256.Sum256(hash)
		hash = sum[:]
	}
	block, _ := aes.NewCipher(hash)
	key, _ := cipher.NewGCM(block)
	n := &noisePeer{hash: hash, salt: append([]byte{}, hash...), key: key}
	n.auth(header)
	return n
}
func (n *noisePeer) auth(data []byte) {
	joined := append(append([]byte{}, n.hash...), data...)
	sum := sha256.Sum256(joined)
	n.hash = sum[:]
}
func (n *noisePeer) mix(secret []byte) error {
	derived, e := kdf.DeriveSecrets(secret, n.salt, nil, 64)
	if e != nil {
		return e
	}
	n.salt = derived[:32]
	block, e := aes.NewCipher(derived[32:])
	if e != nil {
		return e
	}
	n.key, e = cipher.NewGCM(block)
	n.counter = 0
	return e
}
func (n *noisePeer) iv() []byte {
	v := make([]byte, 12)
	binary.BigEndian.PutUint32(v[8:], n.counter)
	n.counter++
	return v
}
func (n *noisePeer) encrypt(data []byte) []byte {
	out := n.key.Seal(nil, n.iv(), data, n.hash)
	n.auth(out)
	return out
}
func (n *noisePeer) decrypt(data []byte) ([]byte, error) {
	out, e := n.key.Open(nil, n.iv(), data, n.hash)
	if e == nil {
		n.auth(data)
	}
	return out, e
}
func (n *noisePeer) split() (cipher.AEAD, cipher.AEAD, error) {
	data, e := kdf.DeriveSecrets(nil, n.salt, nil, 64)
	if e != nil {
		return nil, nil, e
	}
	a, e := aes.NewCipher(data[:32])
	if e != nil {
		return nil, nil, e
	}
	b, e := aes.NewCipher(data[32:])
	if e != nil {
		return nil, nil, e
	}
	write, e := cipher.NewGCM(a)
	if e != nil {
		return nil, nil, e
	}
	read, e := cipher.NewGCM(b)
	return write, read, e
}
func noiseFrame(data []byte) []byte {
	n := len(data)
	return append([]byte{byte(n >> 16), byte(n >> 8), byte(n)}, data...)
}
func parseNoiseFrame(data []byte, first bool) ([]byte, []byte, error) {
	var header []byte
	if first {
		if len(data) < 4 {
			return nil, nil, fmt.Errorf("missing WA header")
		}
		header = append([]byte{}, data[:4]...)
		data = data[4:]
	}
	if len(data) < 3 {
		return nil, nil, fmt.Errorf("truncated frame")
	}
	n := int(data[0])<<16 | int(data[1])<<8 | int(data[2])
	if len(data) != n+3 {
		return nil, nil, fmt.Errorf("frame length mismatch")
	}
	return header, data[3:], nil
}
