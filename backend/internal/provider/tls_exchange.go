package provider

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

// The configuration comes from Go's original-environment trust loader. System
// defaults or caller-supplied verification replacements cannot stand in for
// the pinned Source trust store. mTLS, session caching and HTTP2 are separate.
func verifiedHTTP1TLS(input *tls.Config, hostname string) (*tls.Config, error) {
	if input == nil || input.RootCAs == nil || input.InsecureSkipVerify || input.VerifyConnection != nil || input.VerifyPeerCertificate != nil ||
		len(input.Certificates) > 0 || input.GetClientCertificate != nil || input.ClientSessionCache != nil ||
		input.ServerName != "" && input.ServerName != hostname || len(input.NextProtos) > 1 || len(input.NextProtos) == 1 && input.NextProtos[0] != "http/1.1" ||
		input.MinVersion != 0 && input.MinVersion != tls.VersionTLS12 || input.MaxVersion != 0 || len(input.CipherSuites) > 0 || len(input.CurvePreferences) > 0 ||
		input.Renegotiation != tls.RenegotiateNever || input.KeyLogWriter != nil || len(input.EncryptedClientHelloConfigList) > 0 {
		return nil, fmt.Errorf("%w: verified pinned-root HTTP/1 TLS configuration", ErrHTTPExchangeProfile)
	}
	copy := input.Clone()
	copy.RootCAs = input.RootCAs.Clone()
	copy.ServerName = hostname
	copy.NextProtos = []string{"http/1.1"}
	copy.MinVersion = tls.VersionTLS12
	return copy, nil
}

// TLSConnectionFault retains the pinned Bun native Error's enumerable record.
// Its Go verification diagnostic is private: Source's native cause is absent,
// so exposing another cause link would change retry/policy cause traversal.
type TLSConnectionFault struct {
	code, path, message string
	verification        error
}

func (failure *TLSConnectionFault) Error() string { return failure.message }
func (failure *TLSConnectionFault) Code() string  { return failure.code }
func (failure *TLSConnectionFault) EnumerableErrorJSON() (protocol.JSONObject, error) {
	quote := func(value string) json.RawMessage {
		raw, _ := json.Marshal(value)
		return protocol.StringifyValidatedJavaScriptJSON(raw)
	}
	return protocol.NewJSONObject(
		protocol.JSONField{Name: "code", Value: quote(failure.code)},
		protocol.JSONField{Name: "path", Value: quote(failure.path)},
		protocol.JSONField{Name: "errno", Value: json.RawMessage("0")},
	), nil
}

// Original malformed CA bytes fail while Bun creates fetch's socket, before
// any TCP connection. Keep this native record at the URL-specific exchange
// boundary; the process CA memo itself has no URL or SDK error ownership.
func originalTLSConfigurationFault(err error, endpoint string) error {
	var invalid *tlsconfig.InvalidCertificateError
	if errors.As(err, &invalid) {
		return &TLSConnectionFault{code: "FailedToOpenSocket", path: endpoint,
			message: "Was there a typo in the url or port?", verification: err}
	}
	return fmt.Errorf("%w: original TLS configuration: %v", ErrHTTPExchangeProfile, err)
}

// These two failures have actual original Source HTTPS observations. Other
// certificate/alert/socket domains remain explicit capabilities until their
// native code/message and request ownership are independently reproduced.
func originalTLSFault(err error, endpoint string) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var verification *tls.CertificateVerificationError
	if errors.As(err, &verification) {
		var hostname x509.HostnameError
		if errors.As(verification.Err, &hostname) {
			return &TLSConnectionFault{code: "ERR_TLS_CERT_ALTNAME_INVALID", path: endpoint,
				message: fmt.Sprintf("ERR_TLS_CERT_ALTNAME_INVALID fetching %q. For more information, pass `verbose: true` in the second argument to fetch()", endpoint), verification: err}
		}
		var authority x509.UnknownAuthorityError
		if errors.As(verification.Err, &authority) && len(verification.UnverifiedCertificates) == 1 {
			certificate := verification.UnverifiedCertificates[0]
			if bytes.Equal(certificate.RawSubject, certificate.RawIssuer) && certificate.CheckSignature(certificate.SignatureAlgorithm, certificate.RawTBSCertificate, certificate.Signature) == nil {
				return &TLSConnectionFault{code: "DEPTH_ZERO_SELF_SIGNED_CERT", path: endpoint, message: "self signed certificate", verification: err}
			}
		}
	}
	return fmt.Errorf("%w: native TLS verification/alert/socket fault %T", ErrHTTPExchangeProfile, err)
}
