package provider

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type metadataExchangeFunc func(*http.Request) (*HTTPExchangeResponse, error)

func (call metadataExchangeFunc) Do(request *http.Request) (*HTTPExchangeResponse, error) {
	return call(request)
}

type metadataCloseSpy struct{ closed bool }

func (body *metadataCloseSpy) Read([]byte) (int, error) { return 0, io.EOF }
func (body *metadataCloseSpy) Close() error             { body.closed = true; return nil }

func pipeExchange(wire string, counter *atomic.Int64) *RawHTTP1Exchange {
	return &RawHTTP1Exchange{DialContext: func(context.Context, string, string) (net.Conn, error) {
		if counter != nil {
			counter.Add(1)
		}
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			request, err := http.ReadRequest(bufio.NewReader(server))
			if err != nil {
				return
			}
			_, _ = io.Copy(io.Discard, request.Body)
			_, _ = io.WriteString(server, wire)
		}()
		return client, nil
	}}
}
func exchangeRequest(t *testing.T) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, "http://fixture.invalid/v1/messages", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func TestRawHTTP1ExchangePreservesOriginalCloseAndReaderFraming(t *testing.T) {
	for _, item := range []struct{ name, wire, body string }{
		{"content-length", "HTTP/1.1 200 OK\r\nX-z: first\r\nContent-Length: 4\r\nConnection: keep-alive, CLOSE\r\nX-z: last\r\n\r\nbody", "body"},
		{"chunked", "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n2\r\nbo\r\n2\r\ndy\r\n0\r\nX-trailer: ignored\r\n\r\n", "body"},
		{"eof", "HTTP/1.0 200 OK\r\nX-raw: value\r\n\r\nbody", "body"},
		{"identity", "HTTP/1.1 200 OK\r\nContent-Length: 0\r\nContent-Encoding: identity, Identity\r\n\r\n", ""},
	} {
		t.Run(item.name, func(t *testing.T) {
			var sends atomic.Int64
			response, err := pipeExchange(item.wire, &sends).Do(exchangeRequest(t))
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Response.Body)
			if err != nil || string(body) != item.body {
				t.Fatalf("same reader lost body %q %v", body, err)
			}
			_ = response.Response.Body.Close()
			if sends.Load() != 1 {
				t.Fatal("physical request replayed")
			}
			if item.name == "content-length" {
				raw, _ := response.OriginalHeaders.MarshalJSON()
				if string(raw) != `{"content-length":"4","connection":"keep-alive, CLOSE","x-z":"last"}` {
					t.Fatal(string(raw))
				}
			}
			if item.name == "eof" {
				if _, exists, _ := response.OriginalHeaders.Get("connection"); exists {
					t.Fatal("invented connection from Response.Close")
				}
			}
		})
	}
}

func TestRawHTTP1ExchangeRejectsUnimplementedCapabilities(t *testing.T) {
	for _, wire := range []string{
		"HTTP/1.1 100 Continue\r\n\r\n",
		"HTTP/1.1 302 Redirect\r\nContent-Length: 0\r\n\r\n",
		"HTTP/1.1 200 OK\r\n folded\r\n\r\n",
		"HTTP/1.1 200 OK\r\nbroken\r\n\r\n",
	} {
		if _, err := pipeExchange(wire, nil).Do(exchangeRequest(t)); !errors.Is(err, ErrHTTPExchangeProfile) {
			t.Fatalf("capability must fail closed for %q: %v", wire, err)
		}
	}
	for _, request := range []*http.Request{nil, {URL: &url.URL{Scheme: "https", Host: "fixture.invalid"}}, {URL: &url.URL{Scheme: "http"}}, {URL: &url.URL{Scheme: "http", Host: "fixture.invalid", User: url.User("credential")}}} {
		if _, err := (&RawHTTP1Exchange{}).Do(request); !errors.Is(err, ErrHTTPExchangeProfile) {
			t.Fatal(err)
		}
	}
	request := exchangeRequest(t)
	request.Header.Set("Upgrade", "websocket")
	if _, err := (&RawHTTP1Exchange{}).Do(request); !errors.Is(err, ErrHTTPExchangeProfile) {
		t.Fatal(err)
	}
	for _, limit := range []int{-1, 3, 1024*1024 + 1} {
		if _, err := (&RawHTTP1Exchange{MaxHeaderBytes: limit}).Do(exchangeRequest(t)); !errors.Is(err, ErrHTTPExchangeProfile) {
			t.Fatal(err)
		}
	}
	bounded := pipeExchange("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n", nil)
	bounded.MaxHeaderBytes = 8
	if _, err := bounded.Do(exchangeRequest(t)); !errors.Is(err, ErrHTTPExchangeProfile) {
		t.Fatal(err)
	}
}

func TestRawHTTP1ExchangePreservesTransportAndMalformedResponseFailures(t *testing.T) {
	cause := errors.New("native dial sentinel")
	if _, err := (&RawHTTP1Exchange{DialContext: func(context.Context, string, string) (net.Conn, error) { return nil, cause }}).Do(exchangeRequest(t)); err != cause {
		t.Fatal("native cause replaced", err)
	}
	for _, wire := range []string{"incomplete", "bad status\r\nX: y\r\n\r\n", "HTTP/1.1 200 OK\r\nbad name: value\r\n\r\n", "HTTP/1.1 200 OK\r\nx: bad\x00value\r\n\r\n"} {
		if _, err := pipeExchange(wire, nil).Do(exchangeRequest(t)); err == nil {
			t.Fatalf("malformed response accepted %q", wire)
		}
	}
	writeFailure := &RawHTTP1Exchange{DialContext: func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		_ = server.Close()
		return client, nil
	}}
	if _, err := writeFailure.Do(exchangeRequest(t)); err == nil {
		t.Fatal("request write failure hidden")
	}
	response, err := pipeExchange("HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\nshort", nil).Do(exchangeRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(response.Response.Body); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal("body failure lost", err)
	}
	_ = response.Response.Body.Close()
}

func TestRawHTTP1ExchangeCancellationAndDefaultPort(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	request := exchangeRequest(t).WithContext(ctx)
	closed := make(chan struct{})
	readStarted := make(chan struct{})
	observedAddress := ""
	exchange := &RawHTTP1Exchange{DialContext: func(_ context.Context, network, address string) (net.Conn, error) {
		observedAddress = network + ":" + address
		client, server := net.Pipe()
		go func() {
			defer close(closed)
			defer server.Close()
			parsed, err := http.ReadRequest(bufio.NewReader(server))
			if err != nil {
				return
			}
			_, _ = io.Copy(io.Discard, parsed.Body)
			close(readStarted)
			_, _ = io.Copy(io.Discard, server)
		}()
		return client, nil
	}}
	done := make(chan error, 1)
	go func() { _, err := exchange.Do(request); done <- err }()
	select {
	case <-readStarted:
	case <-time.After(time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancellation hidden")
		}
	case <-time.After(time.Second):
		t.Fatal("header read not cancellable")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("connection leaked")
	}
	if observedAddress != "tcp:fixture.invalid:80" {
		t.Fatal(observedAddress)
	}
}

func TestAnthropicExchangeRequiresOriginalMetadataAndRetainsCause(t *testing.T) {
	for _, result := range []*HTTPExchangeResponse{nil, {}, {Response: &http.Response{}}, {Response: &http.Response{Body: &metadataCloseSpy{}}}} {
		client := NewAnthropic(AnthropicConfig{HTTPExchange: metadataExchangeFunc(func(*http.Request) (*HTTPExchangeResponse, error) { return result, nil })})
		if _, err := client.exchangeHTTP(exchangeRequest(t)); !errors.Is(err, ErrHTTPExchangeProfile) {
			t.Fatal(err)
		}
		if result != nil && result.Response != nil && result.Response.Body != nil && !result.Response.Body.(*metadataCloseSpy).closed {
			t.Fatal("invalid response leaked body")
		}
	}
	boundary := errors.New("original network sentinel")
	client := NewAnthropic(AnthropicConfig{APIKey: "fake", Timeout: time.Second, HTTPExchange: metadataExchangeFunc(func(*http.Request) (*HTTPExchangeResponse, error) { return nil, boundary })})
	_, err := client.OpenMessagesStream(context.Background(), &MessagesRequest{Body: []byte(`{}`)})
	var sdk *SDKStreamError
	if !errors.As(err, &sdk) || sdk.Cause != boundary || !errors.Is(err, boundary) {
		t.Fatalf("native cause ownership lost: %#v %v", sdk, err)
	}
	client = NewAnthropic(AnthropicConfig{APIKey: "fake", Timeout: time.Second, HTTPExchange: &RawHTTP1Exchange{}})
	if _, err := client.CreateMessage(context.Background(), map[string]any{}); !errors.Is(err, ErrHTTPExchangeProfile) {
		t.Fatal("aggregate swallowed explicit profile failure", err)
	}
}
