package tlsconfig

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"testing"
	"time"
)

func TestVerifiedConfigOriginalInputsAndPinnedDefault(t *testing.T) {
	reads := 0
	state, _ := NewState(func(string) ([]byte, error) { reads++; return nil, os.ErrNotExist })
	config, err := state.VerifiedConfig(nil)
	if err != nil || config.RootCAs == nil || config.InsecureSkipVerify || config.MinVersion != tls.VersionTLS12 || len(config.NextProtos) != 1 || config.NextProtos[0] != "http/1.1" || len(config.RootCAs.Subjects()) != 145 {
		t.Fatal(config, err)
	}
	if reads != 0 {
		t.Fatal("default queried a host file")
	}
	next, err := state.VerifiedConfig(nil)
	if err != nil || next.RootCAs == config.RootCAs {
		t.Fatal("verification pool exposed shared mutable reference", err)
	}
	state.Clear()
	config, err = state.VerifiedConfig(map[string]string{"NODE_EXTRA_CA_CERTS": "private-missing"})
	if err != nil || len(config.RootCAs.Subjects()) != 145 || reads != 1 {
		t.Fatal("Source read error must retain runtime bundle", reads, err)
	}
}
func TestActualBunFirstPEMParserProfiles(t *testing.T) {
	var contract struct {
		Rows []struct {
			Name, OriginalExtra string
			Outcome             struct {
				Outcome string
				Error   *struct{ Cause *struct{ Code string } }
			}
			Posts int
		}
	}
	raw, err := os.ReadFile("testdata/tls_pem_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	if len(contract.Rows) != 13 {
		t.Fatal("actual Source parser corpus changed", len(contract.Rows))
	}
	for _, row := range contract.Rows {
		t.Run(row.Name, func(t *testing.T) {
			state, _ := NewState(func(string) ([]byte, error) { return []byte(row.OriginalExtra), nil })
			config, err := state.VerifiedConfig(map[string]string{"NODE_EXTRA_CA_CERTS": "original-private-extra"})
			if row.Outcome.Outcome == "error" {
				if !errors.Is(err, ErrInvalidPEM) || row.Outcome.Error.Cause.Code != "FailedToOpenSocket" || row.Posts != 0 {
					t.Fatal("actual Bun invalid-first boundary", config, err)
				}
				var invalid *InvalidCertificateError
				if !errors.As(err, &invalid) || invalid.Index != 145 || invalid.Error() != "invalid original CA PEM at array item 145" {
					t.Fatal("missing typed physical-creation configuration fact", err)
				}
				if row.Name == "invalid-before-valid" {
					if block, _ := pem.Decode([]byte(row.OriginalExtra)); block == nil {
						t.Fatal("control decoder no longer demonstrates actual Bun mismatch")
					}
				}
				return
			}
			if err != nil || row.Posts != 1 {
				t.Fatal("accepted actual Bun PEM rejected", err)
			}
			certs, err := parseCertificateItem(row.OriginalExtra)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = certs[0].Verify(x509.VerifyOptions{Roots: config.RootCAs, DNSName: "127.0.0.1", CurrentTime: certs[0].NotBefore.Add(time.Hour)}); err != nil {
				t.Fatal("independent original certificate absent from pool", err)
			}
		})
	}
}

func TestCapturedClientArrayDefersParserAndSurvivesProcessClear(t *testing.T) {
	var contract struct {
		Rows []struct{ Name, OriginalExtra string }
	}
	raw, err := os.ReadFile("testdata/tls_pem_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	original := contract.Rows[0].OriginalExtra
	reads := 0
	state, _ := NewState(func(string) ([]byte, error) { reads++; return []byte(original), nil })
	clientArray, err := state.Certificates(map[string]string{"NODE_EXTRA_CA_CERTS": "original-file"})
	if err != nil {
		t.Fatal(err)
	}
	original = "invalid replacement"
	state.Clear()
	newClientArray, err := state.Certificates(map[string]string{"NODE_EXTRA_CA_CERTS": "original-file"})
	if err != nil {
		t.Fatal("Source GetClient does not parse invalid CA", err)
	}
	if clientArray == newClientArray || reads != 2 {
		t.Fatal("client reference was replaced")
	}
	if _, err = VerifiedConfigFromCertificates(clientArray); err != nil {
		t.Fatal("old client's original CA changed", err)
	}
	if _, err = VerifiedConfigFromCertificates(newClientArray); !errors.Is(err, ErrInvalidPEM) {
		t.Fatal("malformed CA must fail at physical creation", err)
	}
	if reads != 2 {
		t.Fatal("physical fetch re-read process files", reads)
	}
	// The array itself remains shared, as in Source fetchOptions.tls.ca. A
	// subsequent physical fetch sees an explicit external mutation of that same
	// captured reference, rather than a prematurely cached certificate pool.
	clientArray.Items[145] = json.RawMessage(`"mutated invalid"`)
	if _, err = VerifiedConfigFromCertificates(clientArray); !errors.Is(err, ErrInvalidPEM) {
		t.Fatal("physical fetch froze live Source array", err)
	}
	if _, err = state.VerifiedConfig(map[string]string{"NODE_OPTIONS": "--use-system-ca"}); err == nil {
		t.Fatal("unported native runtime CA setting was ignored")
	}
}
func TestExplicitUnportedTLSOptionsAndMutableArrayShapes(t *testing.T) {
	for _, key := range []string{"CLAUDE_CODE_CLIENT_CERT", "CLAUDE_CODE_CLIENT_KEY", "CLAUDE_CODE_CLIENT_KEY_PASSPHRASE", "NODE_TLS_REJECT_UNAUTHORIZED", "SSL_CERT_FILE", "SSL_CERT_DIR", "OPENSSL_CONF", "OPENSSL_MODULES"} {
		state, _ := NewState(os.ReadFile)
		_, err := state.VerifiedConfig(map[string]string{key: "original"})
		var capability *CapabilityError
		if !errors.As(err, &capability) {
			t.Fatal(key, err)
		}
	}
	for _, options := range []string{"--use-system-ca", "--use-openssl-ca", "--tls-min-v1.0", "--use-bundled-ca"} {
		state, _ := NewState(os.ReadFile)
		_, err := state.VerifiedConfig(map[string]string{"NODE_OPTIONS": options})
		var capability *CapabilityError
		if !errors.As(err, &capability) {
			t.Fatal(options, err)
		}
	}
	state, _ := NewState(os.ReadFile)
	array, err := state.Certificates(map[string]string{"NODE_EXTRA_CA_CERTS": "missing-original"})
	if err != nil {
		t.Fatal(err)
	}
	array.Items = []json.RawMessage{}
	if _, err = state.VerifiedConfig(nil); err == nil {
		t.Fatal("empty modified array silently used defaults")
	}
	array.Items = []json.RawMessage{json.RawMessage("42")}
	if _, err = state.VerifiedConfig(nil); err == nil {
		t.Fatal("non-string array silently coerced")
	}
	for _, text := range []string{"-----BEGIN PRIVATE KEY-----\nAA==\n-----END PRIVATE KEY-----", "-----BEGIN CERTIFICATE-----\nProc-Type: 4,ENCRYPTED\nAA==\n-----END CERTIFICATE-----"} {
		_, err := parseCertificateItem(text)
		var capability *CapabilityError
		if !errors.As(err, &capability) {
			t.Fatal("unknown parser extension must be explicit", err)
		}
	}
}
