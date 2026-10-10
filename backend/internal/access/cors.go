package access

import (
	"context"
	"strconv"
	"strings"

	weburl "github.com/nlnwa/whatwg-url/url"
)

type CORSResolution struct {
	Allowed  bool              `json:"allowed"`
	Rejected bool              `json:"rejected"`
	Headers  map[string]string `json:"headers"`
}

type CORSOptions struct {
	H5Enabled       bool
	IsOriginAllowed func(context.Context, string) (bool, error)
}

func baseCORSHeaders() map[string]string {
	return map[string]string{
		"Access-Control-Allow-Methods":  "GET, POST, PUT, PATCH, DELETE, OPTIONS",
		"Access-Control-Allow-Headers":  "Content-Type, Authorization",
		"Access-Control-Expose-Headers": "Server-Timing, X-Request-Id",
		"Access-Control-Max-Age":        "86400",
		"Vary":                          "Origin",
	}
}

func CORSHeaders(origin string) map[string]string {
	if origin == "" {
		origin = "http://localhost:3000"
	}
	headers := baseCORSHeaders()
	headers["Access-Control-Allow-Origin"] = origin
	return headers
}

func isLoopbackIPv4(hostname string) bool {
	parts := strings.Split(hostname, ".")
	if len(parts) != 4 || parts[0] != "127" {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return false
			}
		}
		value, err := strconv.ParseUint(part, 10, 64)
		if err != nil || value > 255 {
			return false
		}
	}
	return true
}

// CORS intentionally has its own loopback test: unlike H5 host policy, the
// reference CORS middleware does not accept IPv4-mapped IPv6 loopback hosts.
func isCORSLocalOrigin(origin string) bool {
	if origin == "" || origin == "file://" {
		return true
	}
	parsed, err := weburl.Parse(origin)
	if err != nil || (parsed.Scheme() != "http" && parsed.Scheme() != "https") {
		return false
	}
	hostname := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(parsed.Hostname()), "["), "]"))
	return hostname == "localhost" || hostname == "::1" || isLoopbackIPv4(hostname)
}

func ResolveCORS(ctx context.Context, origin string, options CORSOptions) (CORSResolution, error) {
	if origin == "" || !options.H5Enabled || isCORSLocalOrigin(origin) {
		return CORSResolution{Allowed: true, Headers: CORSHeaders(origin)}, nil
	}
	if options.IsOriginAllowed != nil {
		allowed, err := options.IsOriginAllowed(ctx, origin)
		if err != nil {
			return CORSResolution{}, err
		}
		if allowed {
			return CORSResolution{Allowed: true, Headers: CORSHeaders(origin)}, nil
		}
	}
	return CORSResolution{Rejected: true, Headers: baseCORSHeaders()}, nil
}
