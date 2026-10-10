package provider

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	weburl "github.com/nlnwa/whatwg-url/url"
)

var managedProxyPath = regexp.MustCompile(`^/proxy/providers/[^/]+$`)

// ResolveManagedProviderProxyAccessToken matches the source's final fetch
// boundary. A desktop token authenticates only its host-managed HTTP loopback
// proxy; it cannot follow a changed origin, credentials or a different path.
func ResolveManagedProviderProxyAccessToken(env map[string]string, requestURL string) *string {
	token := javascriptTrim(env["CC_HAHA_LOCAL_ACCESS_TOKEN"])
	baseURL := env["ANTHROPIC_BASE_URL"]
	if !clientEnvTruthy(env["CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST"]) || env["ANTHROPIC_API_KEY"] != "proxy-managed" || baseURL == "" || token == "" {
		return nil
	}
	if requestURL == "" {
		requestURL = baseURL
	}
	base, baseErr := weburl.Parse(baseURL)
	request, requestErr := weburl.Parse(requestURL)
	if baseErr != nil || requestErr != nil {
		return nil
	}
	basePath := strings.TrimRight(base.Pathname(), "/")
	if basePath == "" {
		basePath = "/"
	}
	requestPath := strings.TrimRight(request.Pathname(), "/")
	if requestPath == "" {
		requestPath = "/"
	}
	if base.Scheme() != "http" || base.Hostname() != "127.0.0.1" || base.Username() != "" || base.Password() != "" || base.Search() != "" || base.Hash() != "" ||
		request.Username() != "" || request.Password() != "" || basePath != "/proxy" && !managedProxyPath.MatchString(basePath) ||
		request.Scheme() != base.Scheme() || request.Host() != base.Host() || requestPath != basePath && !strings.HasPrefix(requestPath, basePath+"/") {
		return nil
	}
	return &token
}

func injectFirstPartyRequestID(env map[string]string) bool {
	for _, flag := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		if clientEnvTruthy(env[flag]) {
			return false
		}
	}
	base := env["ANTHROPIC_BASE_URL"]
	if base == "" {
		return true
	}
	url, err := weburl.Parse(base)
	if err != nil {
		return false
	}
	return url.Host() == "api.anthropic.com" || env["USER_TYPE"] == "ant" && url.Host() == "api-staging.anthropic.com"
}

func randomRequestID() string {
	var bytes [16]byte
	_, _ = rand.Read(bytes[:])
	bytes[6] = bytes[6]&0x0f | 0x40
	bytes[8] = bytes[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", bytes[:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:])
}

func copyClientEnvironment(env map[string]string) map[string]string {
	copy := make(map[string]string, len(env))
	for name, value := range env {
		copy[name] = value
	}
	return copy
}

func (options DirectClientOptions) finalFetchHeaders(requestURL string, headers http.Header) (http.Header, error) {
	result := headers.Clone()
	if result == nil {
		result = http.Header{}
	}
	env := options.env
	if options.fetchEnvironment != nil {
		env = options.fetchEnvironment()
	}
	if token := ResolveManagedProviderProxyAccessToken(env, requestURL); token != nil {
		result.Set("Authorization", "Bearer "+*token)
	}
	if options.injectRequestID {
		present := false
		for name, values := range result {
			if strings.EqualFold(name, "x-client-request-id") && len(values) > 0 {
				present = true
				break
			}
		}
		if !present {
			requestID := options.requestID
			if requestID == nil {
				requestID = randomRequestID
			}
			result.Set("x-client-request-id", requestID())
		}
	}
	return result, nil
}
