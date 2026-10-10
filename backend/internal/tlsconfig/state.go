// Package tlsconfig owns the process-lifetime CA memo used by Source caCerts.ts.
// A CLI/Server host retains one State across client and controller instances.
package tlsconfig

import (
	_ "embed"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

//go:embed bun_1_3_14_roots.json
var pinnedRoots []byte

// CertificateArray intentionally preserves the Source array's reference. Host
// code must serialize mutations with calls; State's lock protects its memo,
// not externally concurrent mutations of this JavaScript-compatible array.
type CertificateArray struct {
	Items []json.RawMessage
}

// CapabilityError names a TLS boundary for which no native Source contract has
// been ported. It cannot silently fall back to the developer's OS certificate
// store, an insecure connection or an approximate parser.
type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string { return "TLS configuration capability: " + e.Boundary }

type State struct {
	mu       sync.Mutex
	readFile func(string) ([]byte, error)
	cached   bool
	value    *CertificateArray
}

func NewState(readFile func(string) ([]byte, error)) (*State, error) {
	if readFile == nil {
		return nil, errors.New("TLS configuration requires an explicit file reader")
	}
	return &State{readFile: readFile}, nil
}

// HasNodeOption uses JavaScript /\s+/ splitting, not shell tokenization or Go's
// Unicode White_Space set (which differs for BOM and U+0085).
func HasNodeOption(options, flag string) bool {
	for _, token := range strings.FieldsFunc(options, jsWhitespace) {
		if token == flag {
			return true
		}
	}
	return false
}

func jsWhitespace(r rune) bool {
	return r >= '\t' && r <= '\r' || r == ' ' || r == 0x00a0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
}

// BundledCertificates returns a fresh copy of the exact 145 original Bun
// 1.3.14 strings, including their individual lack of a final newline. This is
// a declared runtime constant, never a Source-prepared request/config input.
func BundledCertificates() *CertificateArray {
	var items []json.RawMessage
	// The embedded immutable resource is checked byte-for-byte by native tests.
	if err := json.Unmarshal(pinnedRoots, &items); err != nil {
		panic("invalid pinned Bun 1.3.14 roots resource: " + err.Error())
	}
	return &CertificateArray{Items: items}
}

func quoteUTF8(data []byte) json.RawMessage {
	data = protocol.DecodeJavaScriptUTF8(data)
	units := []uint16{}
	for len(data) > 0 {
		r, n := utf8.DecodeRune(data)
		data = data[n:]
		if r <= 0xffff {
			units = append(units, uint16(r))
		} else {
			r -= 0x10000
			units = append(units, uint16(0xd800+r>>10), uint16(0xdc00+r&0x3ff))
		}
	}
	return protocol.QuoteJavaScriptUTF16(units)
}

// Certificates mirrors the no-argument process memo. Cached undefined and
// arrays are returned before inspecting changed environment/files, until Clear.
// A read failure is caught by Source and retains the bundled array, not an
// invalid-file fallback at the subsequent native TLS parser boundary.
func (s *State) Certificates(env map[string]string) (*CertificateArray, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached {
		return s.value, nil
	}
	options := env["NODE_OPTIONS"]
	if HasNodeOption(options, "--use-system-ca") || HasNodeOption(options, "--use-openssl-ca") {
		return nil, &CapabilityError{Boundary: "original system CA store"}
	}
	extra := env["NODE_EXTRA_CA_CERTS"]
	if extra != "" {
		s.value = BundledCertificates()
		if raw, err := s.readFile(extra); err == nil {
			s.value.Items = append(s.value.Items, quoteUTF8(raw))
		}
	}
	s.cached = true
	return s.value, nil
}

func (s *State) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cached, s.value = false, nil
}
