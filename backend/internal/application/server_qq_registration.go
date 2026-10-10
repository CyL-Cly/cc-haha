package application

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/imregistration"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

// The connector needs a native transport because its socket inactivity timer
// and Close own the cloned sockets. Proxy/CA come from the frozen startup env.
func desktopQQHTTPClient(environment map[string]string) (*http.Client, error) {
	tls, e := tlsconfig.NewState(os.ReadFile)
	if e != nil {
		return nil, e
	}
	config, e := tls.VerifiedConfig(environment)
	if e != nil {
		return nil, e
	}
	env := map[string]string{}
	for k, v := range environment {
		env[k] = v
	}
	transport := &http.Transport{Proxy: func(req *http.Request) (*url.URL, error) {
		selection := provider.ResolveNativeProxySelection(req.URL.String(), env)
		if selection.Bypass || selection.ProxyURL == "" {
			return nil, nil
		}
		u, e := url.Parse(selection.ProxyURL)
		if e != nil {
			return nil, fmt.Errorf("QQ proxy URL: %w", e)
		}
		return u, nil
	}, DialContext: (&net.Dialer{Timeout: 30 * time.Second}).DialContext, TLSHandshakeTimeout: 10 * time.Second, TLSClientConfig: config, DisableCompression: true}
	return &http.Client{Transport: transport}, nil
}

// This constructor is bound after the producer's own Source and physical
// lifecycle verification. No account/platform URL is configurable here.
func newDesktopQQRegistration(client *http.Client, environment map[string]string) (*imregistration.QQService, error) {
	if client == nil {
		var e error
		client, e = desktopQQHTTPClient(environment)
		if e != nil {
			return nil, e
		}
	}
	return imregistration.NewQQ(imregistration.QQOptions{Client: client})
}
