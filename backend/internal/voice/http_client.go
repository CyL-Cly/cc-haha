package voice

import (
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"net"
	"net/http"
	"net/url"
	"time"
)

// Asset fetch uses the original Bun non-Anthropic proxy/TLS policy. In
// particular, HTTPS proxy precedence applies to every target, including HTTP,
// and ANTHROPIC_UNIX_SOCKET never redirects a speech download.
type assetTransport struct {
	environment map[string]string
	tls         *tlsconfig.State
}

func (t *assetTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	selection := provider.ResolveNativeProxySelection(request.URL.String(), t.environment)
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 30 * time.Second}).DialContext, TLSHandshakeTimeout: 10 * time.Second, DisableKeepAlives: true, DisableCompression: true}
	if selection.ProxyURL != "" && !selection.Bypass {
		proxy, e := url.Parse(selection.ProxyURL)
		if e != nil {
			return nil, e
		}
		transport.Proxy = http.ProxyURL(proxy)
	}
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
		return nil, fmt.Errorf("Voice downloads require owned environment and original TLS producer")
	}
	copy := map[string]string{}
	for k, v := range environment {
		copy[k] = v
	}
	return &http.Client{Transport: &assetTransport{copy, tls}}, nil
}
