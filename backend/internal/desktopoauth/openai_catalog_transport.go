package desktopoauth

import (
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

// Global fetch's process environment is a different boundary from the OAuth
// token endpoint's editable network settings. Never consult Go's ambient
// ProxyFromEnvironment or system CA store for this desktop-owned request.
type openAICatalogTransport struct {
	environment func() map[string]string
	tls         *tlsconfig.State
}

func (t *openAICatalogTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	environment := t.environment()
	selection := provider.ResolveNativeProxySelection(request.URL.String(), environment)
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 30 * time.Second}).DialContext, TLSHandshakeTimeout: 10 * time.Second, DisableKeepAlives: true}
	if selection.ProxyURL != "" && !selection.Bypass {
		proxy, err := url.Parse(selection.ProxyURL)
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(proxy)
	}
	if request.URL.Scheme == "https" {
		configuration, err := t.tls.VerifiedConfig(environment)
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig = configuration
	}
	defer transport.CloseIdleConnections()
	return transport.RoundTrip(request)
}

func (s *Service) openAIModelHTTPClient() (*http.Client, error) {
	if s.catalogHTTPExplicit {
		return s.options.HTTPClient, nil
	}
	s.catalogHTTPOnce.Do(func() {
		state, err := tlsconfig.NewState(s.options.Store.FileSystem().ReadFile)
		if err != nil {
			s.catalogHTTPError = err
			return
		}
		s.catalogHTTP = &http.Client{Transport: &openAICatalogTransport{environment: s.options.Environment, tls: state}}
	})
	return s.catalogHTTP, s.catalogHTTPError
}
