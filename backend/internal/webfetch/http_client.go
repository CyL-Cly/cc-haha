package webfetch

import (
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/andybalholm/brotli"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// AxiosProxyURL mirrors the actual axios dependency proxy-from-env. Axios's
// protocol-specific selection differs from the model SDK proxy producer.
func AxiosProxyURL(raw string, environment map[string]string) string {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	get := func(key string) string {
		if v := environment[strings.ToLower(key)]; v != "" {
			return v
		}
		return environment[strings.ToUpper(key)]
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443", "ftp": "21", "gopher": "70", "ws": "80", "wss": "443"}[u.Scheme]
	}
	host := regexp.MustCompile(`:\d*$`).ReplaceAllString(u.Host, "")
	noProxy := strings.ToLower(get("no_proxy"))
	if noProxy == "*" {
		return ""
	}
	for _, pattern := range strings.FieldsFunc(noProxy, func(r rune) bool { return r == ',' || webJSSpace(r) }) {
		if match := regexp.MustCompile(`^(.+):(\d+)$`).FindStringSubmatch(pattern); match != nil {
			if match[2] != port {
				continue
			}
			pattern = match[1]
		}
		if pattern == "" {
			continue
		}
		if pattern[0] == '.' || pattern[0] == '*' {
			pattern = strings.TrimPrefix(pattern, "*")
			if strings.HasSuffix(host, pattern) {
				return ""
			}
		} else if host == pattern {
			return ""
		}
	}
	proxy := get(u.Scheme + "_proxy")
	if proxy == "" {
		proxy = get("all_proxy")
	}
	if proxy != "" && !strings.Contains(proxy, "://") {
		proxy = u.Scheme + "://" + proxy
	}
	return proxy
}

type ownedWebTransport struct {
	environment map[string]string
	tls         *tlsconfig.State
}

func (t *ownedWebTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	// Each request's transport is explicit. No Go ambient ProxyFromEnvironment,
	// default certificate store or developer-specific Dialer is consulted.
	transport := &http.Transport{Proxy: func(req *http.Request) (*url.URL, error) {
		selected := AxiosProxyURL(req.URL.String(), t.environment)
		if selected == "" {
			return nil, nil
		}
		return url.Parse(selected)
	}, DialContext: (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSHandshakeTimeout: 10 * time.Second, DisableCompression: true, DisableKeepAlives: true}
	if request.URL.Scheme == "https" {
		config, e := t.tls.VerifiedConfig(t.environment)
		if e != nil {
			return nil, e
		}
		transport.TLSClientConfig = config
	}
	return transport.RoundTrip(request)
}
func NewHTTPClient(environment map[string]string, tls *tlsconfig.State) (*http.Client, error) {
	if environment == nil || tls == nil {
		return nil, errors.New("Web HTTP client requires owned environment and TLS memo")
	}
	copy := map[string]string{}
	for key, value := range environment {
		copy[key] = value
	}
	return &http.Client{Transport: &ownedWebTransport{environment: copy, tls: tls}}, nil
}

func decodedHTTPBody(response *http.Response) (io.Reader, io.Closer, error) {
	switch strings.ToLower(response.Header.Get("Content-Encoding")) {
	case "gzip", "x-gzip", "compress", "x-compress":
		reader, e := gzip.NewReader(response.Body)
		return reader, reader, e
	case "deflate":
		// Axios inserts the zlib header for the raw-deflate form. An actual
		// buffered prefix keeps this fallback from consuming the response twice.
		reader := newPrefixReader(response.Body)
		if reader.Err != nil {
			return nil, nil, reader.Err
		}
		if len(reader.Prefix) > 0 && reader.Prefix[0]&15 == 8 {
			r, e := zlib.NewReader(reader)
			return r, r, e
		}
		r := flate.NewReader(reader)
		return r, r, nil
	case "br":
		return brotli.NewReader(response.Body), nil, nil
	default:
		return response.Body, nil, nil
	}
}

type prefixReader struct {
	Prefix []byte
	Body   io.Reader
	Err    error
}

func newPrefixReader(reader io.Reader) *prefixReader {
	p := &prefixReader{Body: reader}
	b := make([]byte, 1)
	n, e := reader.Read(b)
	p.Prefix = b[:n]
	if e != nil && e != io.EOF {
		p.Err = e
	}
	return p
}
func (p *prefixReader) Read(data []byte) (int, error) {
	if len(p.Prefix) > 0 {
		n := copy(data, p.Prefix)
		p.Prefix = p.Prefix[n:]
		return n, nil
	}
	return p.Body.Read(data)
}

// Only the first six fractional base-36 digits enter Source's filename. Stop
// earlier when the original Number's fractional expansion is exact (e.g. .5
// produces "i"). No second RNG or environment read is introduced.
func RandomBase36Suffix(value float64) string {
	if value <= 0 || value >= 1 || math.IsNaN(value) {
		return ""
	}
	var out strings.Builder
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	for i := 0; i < 6; i++ {
		value *= 36
		digit := int(value)
		if digit > 35 {
			digit = 35
		}
		out.WriteByte(digits[digit])
		value -= float64(digit)
		if value == 0 {
			break
		}
	}
	return out.String()
}

func webJSSpace(r rune) bool {
	return r >= 9 && r <= 13 || r == 32 || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
}

// ReadDecodedResponse shares the original native HTTP content decoding with
// preflight JSON and external web-search fetch callers.
func ReadDecodedResponse(response *http.Response) ([]byte, error) {
	reader, closer, e := decodedHTTPBody(response)
	if e != nil {
		return nil, e
	}
	if closer != nil {
		defer closer.Close()
	}
	return io.ReadAll(reader)
}
