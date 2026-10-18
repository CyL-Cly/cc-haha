package provider

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
)

var ErrHTTPExchangeProfile = errors.New("HTTP exchange capability is not migrated")

type HTTPExchangeResponse struct {
	Response        *http.Response
	OriginalHeaders *BunHeaders
	openAI          *openAIStreamBinding
	localBody       bool
}
type HTTPExchange interface {
	Do(*http.Request) (*HTTPExchangeResponse, error)
}

func (client *Anthropic) exchangeHTTP(request *http.Request) (*HTTPExchangeResponse, error) {
	if client.config.HTTPExchange != nil {
		result, err := client.config.HTTPExchange.Do(request)
		if err != nil {
			return nil, err
		}
		if result == nil || result.Response == nil || result.Response.Body == nil || result.OriginalHeaders == nil {
			if result != nil && result.Response != nil && result.Response.Body != nil {
				_ = result.Response.Body.Close()
			}
			return nil, fmt.Errorf("%w: original response/body/header record", ErrHTTPExchangeProfile)
		}
		return result, nil
	}
	response, err := client.client.Do(request)
	if err != nil {
		return nil, err
	}
	return &HTTPExchangeResponse{Response: response}, nil
}

// RawHTTP1Exchange is an explicit direct HTTP/1, fresh-connection profile. It
// never calls http.Transport, infers original headers from Response.Close, or
// retries a physical send. HTTPS requires an explicit verified configuration;
// Plain HTTP proxies use absolute-form requests; HTTPS uses a CONNECT tunnel
// with the same verified target trust. Authenticated/HTTPS proxies, pooling,
// redirects remain explicit capability boundaries. Entity decoding is lazy and
// retains the original physical response headers.
type RawHTTP1Exchange struct {
	ProxyURL       string
	DialContext    func(context.Context, string, string) (net.Conn, error)
	MaxHeaderBytes int
	VerifiedTLS    *tls.Config
	// NativeTLSOptions distinguishes Source's explicit fetch tls.ca object from
	// the runtime default TLS store. Equal certificates do not merge those pools.
	NativeTLSOptions bool
	// ResolveVerifiedTLS runs at physical fetch, after GetClient has captured
	// its process-owned original CA array. A malformed CA must not become an
	// earlier client-construction failure or be re-read between retry attempts.
	ResolveVerifiedTLS func() (*tls.Config, error)
}

func (exchange *RawHTTP1Exchange) Do(request *http.Request) (*HTTPExchangeResponse, error) {
	if request == nil || request.URL == nil || request.URL.Scheme != "http" && request.URL.Scheme != "https" || request.URL.Hostname() == "" || request.URL.User != nil {
		return nil, fmt.Errorf("%w: direct HTTP/1 URL without proxy/userinfo", ErrHTTPExchangeProfile)
	}
	request = nativeFetchRequest(request)
	var verified *tls.Config
	if request.URL.Scheme == "https" {
		input := exchange.VerifiedTLS
		if exchange.ResolveVerifiedTLS != nil {
			if input != nil {
				return nil, fmt.Errorf("%w: ambiguous TLS trust owner", ErrHTTPExchangeProfile)
			}
			var err error
			input, err = exchange.ResolveVerifiedTLS()
			if err != nil {
				return nil, originalTLSConfigurationFault(err, request.URL.String())
			}
		}
		var err error
		verified, err = verifiedHTTP1TLS(input, request.URL.Hostname())
		if err != nil {
			return nil, err
		}
	}
	if request.Header.Get("Upgrade") != "" {
		return nil, fmt.Errorf("%w: protocol upgrade", ErrHTTPExchangeProfile)
	}
	limit := exchange.MaxHeaderBytes
	if limit == 0 {
		limit = 64 * 1024
	}
	if limit < 4 || limit > 1024*1024 {
		return nil, fmt.Errorf("%w: bounded original header capture", ErrHTTPExchangeProfile)
	}
	dial := exchange.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	address := nativeTargetAuthority(request.URL)
	if exchange.ProxyURL != "" {
		var err error
		address, err = nativeProxyAuthority(exchange.ProxyURL)
		if err != nil {
			return nil, err
		}
	}
	connection, err := dial(request.Context(), "tcp", address)
	if err != nil {
		return nil, err
	}
	if verified != nil {
		if exchange.ProxyURL != "" {
			if err := negotiateNativeProxy(request.Context(), connection, nativeTargetAuthority(request.URL), limit); err != nil {
				_ = connection.Close()
				return nil, err
			}
		}
		secured := tls.Client(connection, verified)
		if err := secured.HandshakeContext(request.Context()); err != nil {
			_ = secured.Close()
			return nil, originalTLSFault(err, request.URL.String())
		}
		connection = secured
	}
	lease := &http1Body{connection: connection, finished: make(chan struct{})}
	success := false
	defer func() {
		if !success {
			_ = lease.Close()
		}
	}()
	go func() {
		select {
		case <-request.Context().Done():
			_ = lease.Close()
		case <-lease.finished:
		}
	}()
	write := request.Write
	if exchange.ProxyURL != "" && verified == nil {
		write = request.WriteProxy
	}
	if err := write(connection); err != nil {
		return nil, err
	}
	reader := bufio.NewReaderSize(connection, limit+1)
	var block []byte
	for length := 1; length <= limit; length++ {
		peek, err := reader.Peek(length)
		if err != nil {
			return nil, err
		}
		if length >= 4 && bytes.Equal(peek[length-4:], []byte("\r\n\r\n")) {
			block = append([]byte{}, peek...)
			break
		}
	}
	if block == nil {
		return nil, fmt.Errorf("%w: response headers exceed capture bound", ErrHTTPExchangeProfile)
	}
	lines := bytes.Split(block[:len(block)-4], []byte("\r\n"))
	var rawPairs [][2][]byte
	for _, line := range lines[1:] {
		if len(line) == 0 || line[0] == ' ' || line[0] == '\t' {
			return nil, fmt.Errorf("%w: obsolete header folding", ErrHTTPExchangeProfile)
		}
		colon := bytes.IndexByte(line, ':')
		if colon <= 0 {
			return nil, fmt.Errorf("%w: original header syntax", ErrHTTPExchangeProfile)
		}
		rawPairs = append(rawPairs, [2][]byte{line[:colon], bytes.Trim(line[colon+1:], " \t")})
	}
	pairs, err := HeaderPairsFromWire(rawPairs)
	if err != nil {
		return nil, err
	}
	headers, err := NewFetchHeaders(pairs)
	if err != nil {
		return nil, err
	}
	// Peek did not consume bytes. The same reader owns standard framing and any
	// body bytes read ahead, after the original ordered header snapshot is safe.
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 && response.StatusCode < 400 {
		return nil, fmt.Errorf("%w: informational/upgrade/redirect response", ErrHTTPExchangeProfile)
	}
	encoding, _, _ := headers.Get("content-encoding")
	lease.body = nativeFetchBody(response.Body, encoding, request.URL.String())
	response.Body = lease
	success = true
	return &HTTPExchangeResponse{Response: response, OriginalHeaders: headers}, nil
}

type http1Body struct {
	connection net.Conn
	body       io.ReadCloser
	finished   chan struct{}
	once       sync.Once
	closeErr   error
}

func (body *http1Body) Read(buffer []byte) (int, error) {
	count, err := body.body.Read(buffer)
	if err != nil {
		_ = body.Close()
	}
	return count, err
}
func (body *http1Body) Close() error {
	body.once.Do(func() {
		close(body.finished)
		body.closeErr = body.connection.Close()
		if body.body != nil {
			_ = body.body.Close()
		}
	})
	return body.closeErr
}
