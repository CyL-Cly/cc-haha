package cliauth

import (
	"bytes"
	"crypto/x509"
	"errors"
)

type oauthCertificateError struct {
	cause         error
	message, code string
}

func (e *oauthCertificateError) Error() string { return e.message }
func (e *oauthCertificateError) Unwrap() error { return e.cause }

func cliOAuthTransportError(err error) error {
	var unknown x509.UnknownAuthorityError
	if errors.As(err, &unknown) && unknown.Cert != nil && bytes.Equal(unknown.Cert.RawIssuer, unknown.Cert.RawSubject) && unknown.Cert.CheckSignature(unknown.Cert.SignatureAlgorithm, unknown.Cert.RawTBSCertificate, unknown.Cert.Signature) == nil {
		return &oauthCertificateError{cause: err, message: "self signed certificate", code: "DEPTH_ZERO_SELF_SIGNED_CERT"}
	}
	return err
}
func OAuthSSLErrorHint(err error) string {
	var certificate *oauthCertificateError
	if !errors.As(err, &certificate) {
		return ""
	}
	return "SSL certificate error (" + certificate.code + "). If you are behind a corporate proxy or TLS-intercepting firewall, set NODE_EXTRA_CA_CERTS to your CA bundle path, or ask IT to allowlist *.anthropic.com. Run /doctor for details."
}
func claudeLoginError(err error) string {
	message := "Login failed: " + err.Error() + "\n"
	if hint := OAuthSSLErrorHint(err); hint != "" {
		message += hint + "\n"
	}
	return message
}
