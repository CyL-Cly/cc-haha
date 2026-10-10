package tlsconfig

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var ErrInvalidPEM = errors.New("invalid original CA PEM")

// InvalidCertificateError is a configuration fact, not a synthesized native socket
// error. The exchange owns URL-specific Bun cause projection and request lease.
type InvalidCertificateError struct{ Index int }

func (e *InvalidCertificateError) Error() string {
	return fmt.Sprintf("%s at array item %d", ErrInvalidPEM, e.Index)
}
func (e *InvalidCertificateError) Unwrap() error { return ErrInvalidPEM }

// VerifiedConfig constructs a fresh explicit pool from original environment,
// actual host file bytes, the process memo and the pinned runtime resource.
// No Source-rendered CA array, OS store or prebuilt pool is an input.
func (s *State) VerifiedConfig(env map[string]string) (*tls.Config, error) {
	if err := ValidateOriginalTLSOptions(env); err != nil {
		return nil, err
	}
	array, err := s.Certificates(env)
	if err != nil {
		return nil, err
	}
	return VerifiedConfigFromCertificates(array)
}

// ValidateOriginalTLSOptions is separate from native certificate parsing. A
// client captures Certificates once, then holds that exact Go-produced array.
func ValidateOriginalTLSOptions(env map[string]string) error {
	for _, key := range []string{"CLAUDE_CODE_CLIENT_CERT", "CLAUDE_CODE_CLIENT_KEY", "CLAUDE_CODE_CLIENT_KEY_PASSPHRASE", "NODE_TLS_REJECT_UNAUTHORIZED", "SSL_CERT_FILE", "SSL_CERT_DIR", "OPENSSL_CONF", "OPENSSL_MODULES"} {
		if env[key] != "" {
			return &CapabilityError{Boundary: "original native TLS option " + key}
		}
	}
	for _, option := range strings.FieldsFunc(env["NODE_OPTIONS"], jsWhitespace) {
		if strings.Contains(option, "tls") || strings.Contains(option, "openssl") || strings.Contains(option, "-ca") {
			return &CapabilityError{Boundary: "original native Node TLS option " + option}
		}
	}
	return nil
}

// VerifiedConfigFromCertificates consumes a Go-produced original-env/file
// array without further env lookup or read. Build this pool at physical fetch,
// not GetClient: malformed-CA Source failures occur at fetch creation. Clear
// does not replace an older client's captured reference; nil uses pinned roots.
func VerifiedConfigFromCertificates(array *CertificateArray) (*tls.Config, error) {
	if array == nil {
		array = BundledCertificates()
	}
	if len(array.Items) == 0 {
		return nil, &CapabilityError{Boundary: "externally emptied original CA array"}
	}
	pool := x509.NewCertPool()
	for index, raw := range array.Items {
		units, decodeError := protocol.DecodeJavaScriptJSONString(raw)
		if decodeError != nil {
			return nil, &CapabilityError{Boundary: "non-string original CA item"}
		}
		// PEM is ASCII; retain a leading BOM or other literal prefix rather than
		// trimming it away. Source's native parser rejects BOM+BEGIN on one line.
		text := string(utf16.Decode(units))
		certs, failure := parseCertificateItem(text)
		if failure != nil {
			if errors.Is(failure, ErrInvalidPEM) {
				return nil, &InvalidCertificateError{Index: index}
			}
			return nil, failure
		}
		for _, cert := range certs {
			pool.AddCert(cert)
		}
	}
	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}}, nil
}

// Bun's native CA reader must parse the first certificate: Go pem.Decode skips
// an invalid first block and accepts a later valid block, which is an actual
// counterexample. Once a certificate is read, malformed trailing blocks are
// ignored by the native parser. These boundaries have clean real SDK HTTPS
// observations; unported PEM block kinds/extensions remain explicit errors.
func parseCertificateItem(text string) ([]*x509.Certificate, error) {
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	certs := []*x509.Certificate{}
	for cursor := 0; cursor < len(lines); cursor++ {
		line := strings.TrimSuffix(lines[cursor], "\r")
		if !strings.HasPrefix(line, "-----BEGIN ") {
			continue
		}
		if line != "-----BEGIN CERTIFICATE-----" {
			return nil, &CapabilityError{Boundary: "unported original PEM block kind/label"}
		}
		start := cursor
		for cursor++; cursor < len(lines) && strings.TrimSuffix(lines[cursor], "\r") != "-----END CERTIFICATE-----"; cursor++ {
			if strings.Contains(lines[cursor], ":") {
				return nil, &CapabilityError{Boundary: "original PEM header extension"}
			}
		}
		if cursor == len(lines) {
			break
		}
		block, _ := pem.Decode([]byte(strings.Join(lines[start:cursor+1], "\n")))
		if block == nil {
			break
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			break
		}
		certs = append(certs, cert)
	}
	if len(certs) == 0 {
		return nil, ErrInvalidPEM
	}
	return certs, nil
}
