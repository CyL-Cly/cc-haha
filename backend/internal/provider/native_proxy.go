package provider

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

type NativeProxySelection struct {
	ProxyURL string
	Bypass   bool
}

func IsNativeProxyEnvironment(name string) bool {
	switch name {
	case "HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "NO_PROXY", "no_proxy":
		return true
	}
	return false
}

// ResolveNativeProxySelection ports getProxyUrl/getNoProxy and the Bun target
// bypass branch of Source getProxyFetchOptions. HTTPS has Source priority even
// for an HTTP target. Neither environment lookup uses the ambient Go process.
func ResolveNativeProxySelection(endpoint string, env map[string]string) NativeProxySelection {
	selection := NativeProxySelection{}
	for _, key := range []string{"https_proxy", "HTTPS_PROXY", "http_proxy", "HTTP_PROXY"} {
		if env[key] != "" {
			selection.ProxyURL = env[key]
			break
		}
	}
	noProxy := env["no_proxy"]
	if noProxy == "" {
		noProxy = env["NO_PROXY"]
	}
	selection.Bypass = selection.ProxyURL != "" && endpoint != "" && nativeShouldBypassProxy(endpoint, noProxy)
	return selection
}

func nativeShouldBypassProxy(endpoint, noProxy string) bool {
	if noProxy == "" {
		return false
	}
	if noProxy == "*" {
		return true
	}
	target, e := url.Parse(endpoint)
	if e != nil || target.Scheme == "" || target.Hostname() == "" {
		return false
	}
	host := strings.ToLower(target.Hostname())
	port := target.Port()
	if port == "" {
		if target.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	hostPort := host + ":" + port
	bracketPort := hostPort
	if strings.Contains(host, ":") {
		bracketPort = "[" + host + "]:" + port
	}
	patterns := strings.FieldsFunc(noProxy, func(r rune) bool { return r == ',' || r == '\ufeff' || unicode.IsSpace(r) })
	for _, original := range patterns {
		pattern := strings.ToLower(strings.TrimSpace(original))
		normalized := pattern
		if strings.HasPrefix(pattern, "[") && strings.HasSuffix(pattern, "]") {
			normalized = pattern[1 : len(pattern)-1]
		}
		if strings.Contains(pattern, ":") {
			if match := regexp.MustCompile(`^\[(.+)\]:(\d+)$`).FindStringSubmatch(pattern); match != nil {
				if host == match[1] && port == match[2] {
					return true
				}
				continue
			}
			if strings.Count(pattern, ":") == 1 {
				if pattern == hostPort || pattern == bracketPort {
					return true
				}
				continue
			}
			if host == normalized {
				return true
			}
			continue
		}
		if strings.HasPrefix(pattern, ".") {
			if host == pattern[1:] || strings.HasSuffix(host, pattern) {
				return true
			}
			continue
		}
		if host == normalized {
			return true
		}
	}
	return false
}

// The selected proxy belongs to this request's captured environment. It never
// falls back to a direct connection when proxy negotiation fails.
func NativeDirectHTTPExchangeFromEnvironment(endpoint string, env map[string]string) (*RawHTTP1Exchange, error) {
	selection := ResolveNativeProxySelection(endpoint, env)
	if selection.ProxyURL != "" && !selection.Bypass {
		if _, err := nativeProxyAuthority(selection.ProxyURL); err != nil {
			return nil, err
		}
		return &RawHTTP1Exchange{ProxyURL: selection.ProxyURL}, nil
	}
	return &RawHTTP1Exchange{}, nil
}
