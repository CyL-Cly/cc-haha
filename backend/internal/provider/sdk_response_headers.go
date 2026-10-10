package provider

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var ErrResponseHeaderProfile = errors.New("unsupported response header compatibility profile")

// sdkResponseHeaders is the observed Bun 1.3.14 fetch boundary, before SDK
// WHATWG Headers reads. Unlike new Headers.append, fetch drops empty wire
// values. The tested request-id/custom and singleton fields keep the last
// nonempty value; tested list fields join; Set-Cookie keeps separate rows.
// Unproven duplicate fields fail explicitly instead of guessing a merge rule.
// net/http-removed Connection/Transfer-Encoding/decompression fields still
// require an original HTTP metadata adapter and are not reconstructed here.
func sdkResponseHeaders(raw http.Header) (http.Header, error) {
	result := make(http.Header)
	for name, values := range raw {
		key := http.CanonicalHeaderKey(name)
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("%w: case-alias ordering for %s", ErrResponseHeaderProfile, name)
		}
		var nonempty []string
		for _, value := range values {
			value = strings.Trim(value, " \t\r\n")
			if value != "" {
				nonempty = append(nonempty, value)
			}
		}
		if len(nonempty) == 0 {
			continue
		}
		if len(nonempty) > 1 {
			switch strings.ToLower(name) {
			case "request-id", "x-future", "server", "retry-after", "www-authenticate", "x-request-id", "x-anthropic-future", "anthropic-ratelimit-requests-limit", "anthropic-ratelimit-tokens-limit":
				nonempty = nonempty[len(nonempty)-1:]
			case "content-type", "cache-control", "date", "location", "content-encoding", "access-control-allow-origin":
				nonempty = []string{strings.Join(nonempty, ", ")}
			case "set-cookie":
			default:
				return nil, fmt.Errorf("%w: duplicate %s", ErrResponseHeaderProfile, name)
			}
		}
		result[key] = nonempty
	}
	return result, nil
}
