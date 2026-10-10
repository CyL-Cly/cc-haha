package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// NativeFetchOptions controls the underlying fetch, independently of the
// Anthropic SDK's guarded response profile. Body replay is limited to actual
// request bytes captured by http.NewRequest's GetBody producer.
type NativeFetchOptions struct {
	Redirect        string
	ResolveExchange func(*url.URL) (*RawHTTP1Exchange, error)
}

type NativeFetchRedirectError struct{ message, code string }

func (e *NativeFetchRedirectError) Error() string { return e.message }
func (e *NativeFetchRedirectError) Code() string  { return e.code }

type NativeFetchAbortError struct{ cause error }

func (e *NativeFetchAbortError) Error() string   { return "The operation was aborted." }
func (e *NativeFetchAbortError) Unwrap() error   { return e.cause }
func NewNativeFetchAbortError(cause error) error { return &NativeFetchAbortError{cause} }

func (driver *OwnerNativeIO) OpenFetchWithOptions(request *http.Request, exchange *RawHTTP1Exchange, options NativeFetchOptions) (*HTTPExchangeResponse, error) {
	if request == nil || request.URL == nil || exchange == nil {
		return nil, fmt.Errorf("%w: original fetch request/exchange", ErrHTTPExchangeProfile)
	}
	mode := options.Redirect
	if mode == "" {
		mode = "follow"
	}
	if mode != "follow" && mode != "error" && mode != "manual" {
		return nil, fmt.Errorf("%w: original fetch redirect mode", ErrHTTPExchangeProfile)
	}
	current := request
	for count := 0; ; count++ {
		selected := *exchange
		if current.URL.Scheme == "http" {
			selected.VerifiedTLS, selected.ResolveVerifiedTLS = nil, nil
		}
		actual := &selected
		if options.ResolveExchange != nil {
			var err error
			actual, err = options.ResolveExchange(current.URL)
			if err != nil {
				return nil, err
			}
		}
		config := AnthropicConfig{HTTPExchange: actual, nativeFetchClock: &nativeFetchClock{driver: driver}}
		response, err := driver.openResponse(current.Context(), current.Context(), current, config, 0, true)
		if err != nil {
			if current.Context().Err() != nil && !IsOwnerWaitFailure(err) {
				return nil, &NativeFetchAbortError{context.Cause(current.Context())}
			}
			return nil, err
		}
		status := response.Response.StatusCode
		redirect := status == 301 || status == 302 || status == 303 || status == 307 || status == 308
		if !redirect || mode == "manual" {
			return response, nil
		}
		if mode == "error" {
			_ = response.Response.Body.Close()
			return nil, &NativeFetchRedirectError{fmt.Sprintf("UnexpectedRedirect fetching %q. For more information, pass `verbose: true` in the second argument to fetch()", current.URL.String()), "UnexpectedRedirect"}
		}
		location := response.Response.Header.Get("Location")
		if location == "" {
			return response, nil
		}
		_ = response.Response.Body.Close()
		if count >= 126 {
			return nil, &NativeFetchRedirectError{"The response redirected too many times. For more information, pass `verbose: true` in the second argument to fetch()", "TooManyRedirects"}
		}
		ref, err := url.Parse(location)
		if err != nil {
			return nil, err
		}
		target := current.URL.ResolveReference(ref)
		if target.Scheme != "http" && target.Scheme != "https" || target.User != nil {
			return nil, fmt.Errorf("%w: original redirect URL protocol/credentials", ErrHTTPExchangeProfile)
		}
		method := current.Method
		headers := current.Header.Clone()
		var body io.ReadCloser
		getBody := current.GetBody
		length := current.ContentLength
		if status == 303 && method != "GET" && method != "HEAD" || (status == 301 || status == 302) && method == "POST" {
			method, getBody, length = "GET", nil, 0
			for _, name := range []string{"Content-Encoding", "Content-Language", "Content-Location", "Content-Type", "Content-Length"} {
				headers.Del(name)
			}
		} else if current.Body != nil && current.Body != http.NoBody {
			if getBody == nil {
				return nil, fmt.Errorf("%w: original redirect streaming body replay", ErrHTTPExchangeProfile)
			}
			body, err = getBody()
			if err != nil {
				return nil, err
			}
		}
		if !nativeFetchSameOrigin(current.URL, target) {
			for _, name := range []string{"Authorization", "Cookie", "Proxy-Authorization", "WWW-Authenticate"} {
				headers.Del(name)
			}
		}
		next, err := http.NewRequestWithContext(current.Context(), method, target.String(), body)
		if err != nil {
			if body != nil {
				_ = body.Close()
			}
			return nil, err
		}
		next.Header, next.GetBody, next.ContentLength = headers, getBody, length
		current = next
	}
}

func nativeFetchSameOrigin(a, b *url.URL) bool {
	port := func(v *url.URL) string {
		if v.Port() != "" {
			return v.Port()
		}
		if v.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}

func nativeFetchHeaders(raw []byte, status int) (*BunHeaders, error) {
	if status < 200 {
		return nil, fmt.Errorf("%w: original informational/upgrade response", ErrHTTPExchangeProfile)
	}
	return nativeOwnerHeaderRows(raw)
}

var _ error = (*NativeFetchRedirectError)(nil)
var _ error = (*NativeFetchAbortError)(nil)
