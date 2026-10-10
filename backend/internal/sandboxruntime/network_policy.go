package sandboxruntime

import (
	"context"
	"net"
	"regexp"
	"strings"
	"unicode/utf16"

	whatwg "github.com/nlnwa/whatwg-url/url"
)

var dnsHost = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func stripBrackets(host string) string {
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		return host[1 : len(host)-1]
	}
	return host
}
func ValidHost(host string) bool {
	if host == "" || len(utf16.Encode([]rune(host))) > 255 {
		return false
	}
	bare := stripBrackets(host)
	if strings.Contains(bare, "%") {
		return false
	}
	if net.ParseIP(bare) != nil {
		return true
	}
	return dnsHost.MatchString(bare)
}
func CanonicalHost(host string) (string, bool) {
	bare := stripBrackets(host)
	value := bare
	if ip := net.ParseIP(bare); ip != nil && strings.Contains(bare, ":") {
		value = "[" + bare + "]"
	}
	parsed, e := whatwg.Parse("http://" + value + "/")
	if e != nil {
		return "", false
	}
	return strings.TrimSuffix(stripBrackets(parsed.Hostname()), "."), true
}
func MatchesDomain(host, pattern string) bool {
	host = strings.ToLower(host)
	if strings.HasPrefix(pattern, "*.") {
		if net.ParseIP(stripBrackets(host)) != nil {
			return false
		}
		return strings.HasSuffix(host, "."+strings.ToLower(pattern[2:]))
	}
	return host == strings.ToLower(pattern)
}

// FilterNetworkRequest is the installed manager's deny→allow→ask sequence.
// No-match asks retain the original host/port rather than its canonical key.
func FilterNetworkRequest(ctx context.Context, config *NetworkConfig, host string, port int, ask func(context.Context, string, int) (bool, error)) bool {
	if config == nil || !ValidHost(host) {
		return false
	}
	canonical, ok := CanonicalHost(host)
	if !ok {
		canonical = host
	}
	for _, pattern := range config.DeniedDomains {
		if MatchesDomain(canonical, pattern) {
			return false
		}
	}
	for _, pattern := range config.AllowedDomains {
		if MatchesDomain(canonical, pattern) {
			return true
		}
	}
	if ask == nil {
		return false
	}
	result, e := ask(ctx, host, port)
	return e == nil && result
}
