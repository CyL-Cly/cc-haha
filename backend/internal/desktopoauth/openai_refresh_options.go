package desktopoauth

// OpenAIRefreshOptions is the generic fetch routing used by the bundled token
// client. TLS remains supplied by the existing native TLS state.
type OpenAIRefreshOptions struct {
	Proxy     string `json:"proxy,omitempty"`
	KeepAlive *bool  `json:"keepalive,omitempty"`
}

// OpenAIRefreshFetchOptions mirrors getProxyFetchOptions without targetUrl or
// forAnthropicAPI. This route does not apply an API target's NO_PROXY bypass or
// ANTHROPIC_UNIX_SOCKET. An explicit empty override forces direct transport.
func OpenAIRefreshFetchOptions(environment map[string]string, proxyOverride *string, keepAliveDisabled bool) OpenAIRefreshOptions {
	result := OpenAIRefreshOptions{}
	if proxyOverride != nil {
		result.Proxy = *proxyOverride
	} else {
		for _, key := range []string{"https_proxy", "HTTPS_PROXY", "http_proxy", "HTTP_PROXY"} {
			if environment[key] != "" {
				result.Proxy = environment[key]
				break
			}
		}
	}
	if keepAliveDisabled {
		value := false
		result.KeepAlive = &value
	}
	return result
}
