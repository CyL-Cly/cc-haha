package queryhooks

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

type HTTPHookPolicy struct{ AllowedURLs, AllowedEnvironment *[]string }
type HTTPHookRequest struct {
	Hook        protocol.JSONObject
	Input       string
	Environment map[string]string
	Policy      HTTPHookPolicy
	// TLS and sandbox/proxy policy come from the actual owning startup. An
	// unsupported configured transport remains explicit before any network I/O.
	TLSConfig    *tls.Config
	SandboxProxy string
}
type HTTPHookOutput struct {
	OK         bool
	StatusCode *int
	Body       string
	Error      string
	Aborted    bool
}

func HookURLMatchesPattern(target, pattern string) bool {
	var text strings.Builder
	for _, r := range pattern {
		if r == '*' {
			text.WriteString("[^\r\n\u2028\u2029]*")
		} else {
			text.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	re, e := regexp.Compile("^" + text.String() + "$")
	return e == nil && re.MatchString(target)
}
func InterpolateHookHeader(value string, allowed map[string]bool, env map[string]string) string {
	re := regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)\}|\$([A-Z_][A-Z0-9_]*)`)
	text := re.ReplaceAllStringFunc(value, func(match string) string {
		parts := re.FindStringSubmatch(match)
		name := parts[1]
		if name == "" {
			name = parts[2]
		}
		if allowed[name] {
			return env[name]
		}
		return ""
	})
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == 0 {
			return -1
		}
		return r
	}, text)
}
func BlockedHookAddress(address string) bool {
	parsed, e := netip.ParseAddr(address)
	if e != nil {
		return false
	}
	ip := net.IP(parsed.AsSlice())
	if v4 := ip.To4(); v4 != nil {
		a, b := v4[0], v4[1]
		return a == 0 || a == 10 || a == 100 && b >= 64 && b <= 127 || a == 169 && b == 254 || a == 172 && b >= 16 && b <= 31 || a == 192 && b == 168
	}
	return ip.IsUnspecified() || ip[0]&0xfe == 0xfc || ip[0] == 0xfe && ip[1]&0xc0 == 0x80
}
func HTTPHookHeaders(hook protocol.JSONObject, policy HTTPHookPolicy, env map[string]string) http.Header {
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	var hookNames []string
	_ = json.Unmarshal(hook.Get("allowedEnvVars"), &hookNames)
	allowed := map[string]bool{}
	for _, name := range hookNames {
		if policy.AllowedEnvironment != nil {
			found := false
			for _, candidate := range *policy.AllowedEnvironment {
				if name == candidate {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		allowed[name] = true
	}
	values, _ := hook.Object("headers")
	for _, p := range values.OwnProperties() {
		var name, value string
		_ = json.Unmarshal(p.NameJSON, &name)
		_ = json.Unmarshal(p.Value, &value)
		headers.Set(name, InterpolateHookHeader(value, allowed, env))
	}
	return headers
}

// RunHTTPHook performs the original single HTTP POST primitive. Hook output
// parsing and event/tool decisions belong to the owning shared Harness.
func RunHTTPHook(ctx context.Context, r HTTPHookRequest) (HTTPHookOutput, error) {
	target := r.Hook.String("url")
	if r.Policy.AllowedURLs != nil {
		matched := false
		for _, pattern := range *r.Policy.AllowedURLs {
			matched = matched || HookURLMatchesPattern(target, pattern)
		}
		if !matched {
			return HTTPHookOutput{Error: "HTTP hook blocked: " + target + " does not match any pattern in allowedHttpHookUrls"}, nil
		}
	}
	selection := provider.ResolveNativeProxySelection(target, r.Environment)
	if r.SandboxProxy != "" || selection.ProxyURL != "" && !selection.Bypass {
		return HTTPHookOutput{}, unsupported("HTTP hook actual sandbox/proxy transport")
	}
	for _, name := range []string{"NODE_EXTRA_CA_CERTS", "CLAUDE_CODE_CLIENT_CERT", "CLAUDE_CODE_CLIENT_KEY", "CLAUDE_CODE_CLIENT_KEY_PASSPHRASE"} {
		if r.Environment[name] != "" && r.TLSConfig == nil {
			return HTTPHookOutput{}, unsupported("HTTP hook actual TLS config " + name)
		}
	}
	parsed, e := url.Parse(target)
	if e != nil || parsed.Host == "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return HTTPHookOutput{Error: "Unsupported protocol or invalid HTTP hook URL"}, nil
	}
	timeout := float64(10 * 60 * 1000)
	var seconds float64
	if json.Unmarshal(r.Hook.Get("timeout"), &seconds) == nil && seconds != 0 {
		timeout = seconds * 1000
	}
	active, cancel := context.WithTimeout(ctx, time.Duration(timeout*float64(time.Millisecond)))
	defer cancel()
	transport := &http.Transport{Proxy: nil, TLSClientConfig: r.TLSConfig, ForceAttemptHTTP2: false, DisableKeepAlives: true}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		addresses, e := net.DefaultResolver.LookupIPAddr(ctx, host)
		if e != nil {
			return nil, e
		}
		for _, ip := range addresses {
			if BlockedHookAddress(ip.IP.String()) {
				return nil, fmt.Errorf("HTTP hook blocked: %s resolves to %s (private/link-local address). Loopback (127.0.0.1, ::1) is allowed for local dev.", host, ip.IP.String())
			}
		}
		var failure error
		for _, ip := range addresses {
			connection, e := (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if e == nil {
				return connection, nil
			}
			failure = e
		}
		return nil, failure
	}
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, e := http.NewRequestWithContext(active, http.MethodPost, target, strings.NewReader(r.Input))
	if e != nil {
		return HTTPHookOutput{Error: nativeHTTPHookError(e, parsed.Hostname(), parsed.Port())}, nil
	}
	request.Header = HTTPHookHeaders(r.Hook, r.Policy, r.Environment)
	response, e := client.Do(request)
	if e != nil {
		if active.Err() != nil {
			return HTTPHookOutput{Aborted: true}, nil
		}
		return HTTPHookOutput{Error: nativeHTTPHookError(e, parsed.Hostname(), parsed.Port())}, nil
	}
	defer response.Body.Close()
	body, e := io.ReadAll(response.Body)
	if e != nil {
		if active.Err() != nil {
			return HTTPHookOutput{Aborted: true}, nil
		}
		return HTTPHookOutput{Error: e.Error()}, nil
	}
	code := response.StatusCode
	return HTTPHookOutput{OK: code >= 200 && code < 300, StatusCode: &code, Body: string(protocol.DecodeJavaScriptUTF8(body))}, nil
}
func nativeHTTPHookError(e error, host, port string) string {
	var wrapped *url.Error
	if errors.As(e, &wrapped) {
		e = wrapped.Err
	}
	var dns *net.DNSError
	if errors.As(e, &dns) && dns.IsNotFound {
		return "getaddrinfo ENOTFOUND " + dns.Name
	}
	if errors.Is(e, syscall.ECONNREFUSED) {
		return "connect ECONNREFUSED " + net.JoinHostPort(host, port)
	}
	return e.Error()
}
