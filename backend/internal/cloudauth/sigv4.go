// Package cloudauth implements cloud credential and wire primitives using an
// explicit frozen environment. It never falls back to the machine environment.
package cloudauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"
	"unicode"
)

type AWSCredentials struct {
	AccessKeyID        string     `json:"accessKeyId"`
	SecretAccessKey    string     `json:"secretAccessKey"`
	SessionToken       *string    `json:"sessionToken,omitempty"`
	Expiration         *time.Time `json:"expiration,omitempty"`
	OriginalExpiration *time.Time `json:"originalExpiration,omitempty"`
	CredentialScope    string     `json:"credentialScope,omitempty"`
	AccountID          string     `json:"accountId,omitempty"`
}

// SigningRequest has the Smithy HttpRequest shape. In particular, Path is the
// already-escaped URL pathname, and headers retain their original casing.
type SigningRequest struct {
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Query   map[string][]string `json:"query,omitempty"`
	Headers map[string]string   `json:"headers"`
	Body    []byte              `json:"-"`
}

type SignOptions struct {
	Region, Service   string
	Time              time.Time
	DisableChecksum   bool
	DisablePathEscape bool
}

var alwaysUnsignable = map[string]bool{
	"authorization": true, "cache-control": true, "connection": true, "expect": true,
	"from": true, "keep-alive": true, "max-forwards": true, "pragma": true, "referer": true,
	"te": true, "trailer": true, "transfer-encoding": true, "upgrade": true, "user-agent": true, "x-amzn-trace-id": true,
}

// Sign ports @smithy/signature-v4's signRequest (not an SDK retry loop).
// It returns a copy; signing must not mutate the retained request or credentials.
func Sign(input SigningRequest, credentials AWSCredentials, options SignOptions) SigningRequest {
	request := input
	if request.Method == "" {
		request.Method = "GET"
	}
	if request.Path == "" {
		request.Path = "/"
	} else if !strings.HasPrefix(request.Path, "/") {
		request.Path = "/" + request.Path
	}
	request.Headers = make(map[string]string, len(input.Headers)+4)
	for key, value := range input.Headers {
		switch strings.ToLower(key) {
		case "authorization", "date", "x-amz-date":
			continue
		}
		request.Headers[key] = value
	}
	longDate := options.Time.UTC().Format("20060102T150405Z")
	shortDate := longDate[:8]
	request.Headers["x-amz-date"] = longDate
	if credentials.SessionToken != nil && *credentials.SessionToken != "" {
		request.Headers["x-amz-security-token"] = *credentials.SessionToken
	}
	payloadHash := hashHex(request.Body)
	for key, value := range request.Headers {
		if strings.EqualFold(key, "x-amz-content-sha256") {
			payloadHash = value
			break
		}
	}
	hasChecksum := false
	for key := range request.Headers {
		if strings.EqualFold(key, "x-amz-content-sha256") {
			hasChecksum = true
		}
	}
	if !hasChecksum && !options.DisableChecksum {
		request.Headers["x-amz-content-sha256"] = payloadHash
	}
	keys := make([]string, 0, len(request.Headers))
	for key := range request.Headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	canonical := map[string]string{}
	for _, key := range keys {
		name := strings.ToLower(key)
		if alwaysUnsignable[name] || strings.HasPrefix(name, "proxy-") || strings.HasPrefix(name, "sec-") {
			continue
		}
		canonical[name] = collapseWhitespace(request.Headers[key])
	}
	names := make([]string, 0, len(canonical))
	for key := range canonical {
		names = append(names, key)
	}
	sort.Strings(names)
	headerRows := make([]string, len(names))
	for i, key := range names {
		headerRows[i] = key + ":" + canonical[key]
	}
	path := request.Path
	if !options.DisablePathEscape {
		path = canonicalPath(path)
	}
	canonicalRequest := strings.Join([]string{request.Method, path, canonicalQuery(request.Query), strings.Join(headerRows, "\n") + "\n", strings.Join(names, ";"), payloadHash}, "\n")
	scope := shortDate + "/" + options.Region + "/" + options.Service + "/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + longDate + "\n" + scope + "\n" + hashHex([]byte(canonicalRequest))
	key := hmacBytes([]byte("AWS4"+credentials.SecretAccessKey), shortDate)
	key = hmacBytes(key, options.Region)
	key = hmacBytes(key, options.Service)
	key = hmacBytes(key, "aws4_request")
	request.Headers["authorization"] = "AWS4-HMAC-SHA256 Credential=" + credentials.AccessKeyID + "/" + scope + ", SignedHeaders=" + strings.Join(names, ";") + ", Signature=" + hex.EncodeToString(hmacBytes(key, toSign))
	return request
}

func hashHex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
func hmacBytes(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}
func jsSpace(r rune) bool {
	return r >= 9 && r <= 13 || r == 0xfeff || r == 0x2028 || r == 0x2029 || unicode.Is(unicode.Zs, r)
}
func collapseWhitespace(value string) string {
	return strings.Join(strings.FieldsFunc(value, jsSpace), " ")
}
func escapeURI(value string) string {
	const digits = "0123456789ABCDEF"
	var out strings.Builder
	for _, b := range []byte(value) {
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.ContainsRune("-._~", rune(b)) {
			out.WriteByte(b)
		} else {
			out.WriteByte('%')
			out.WriteByte(digits[b>>4])
			out.WriteByte(digits[b&15])
		}
	}
	return out.String()
}
func canonicalPath(path string) string {
	parts := []string{}
	for _, part := range strings.Split(path, "/") {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(parts) > 0 {
				parts = parts[:len(parts)-1]
			}
		default:
			parts = append(parts, part)
		}
	}
	normalized := strings.Join(parts, "/")
	if strings.HasPrefix(path, "/") {
		normalized = "/" + normalized
	}
	if len(parts) > 0 && strings.HasSuffix(path, "/") {
		normalized += "/"
	}
	return strings.ReplaceAll(escapeURI(normalized), "%2F", "/")
}
func canonicalQuery(query map[string][]string) string {
	keys := []string{}
	rows := map[string]string{}
	for key, values := range query {
		if strings.EqualFold(key, "x-amz-signature") {
			continue
		}
		encoded := escapeURI(key)
		keys = append(keys, encoded)
		parts := make([]string, len(values))
		for i, value := range values {
			parts[i] = encoded + "=" + escapeURI(value)
		}
		sort.Strings(parts)
		rows[encoded] = strings.Join(parts, "&")
	}
	sort.Strings(keys)
	parts := []string{}
	for _, key := range keys {
		if rows[key] != "" {
			parts = append(parts, rows[key])
		}
	}
	return strings.Join(parts, "&")
}
