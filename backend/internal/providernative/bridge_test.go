package providernative

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

const originalBody = `{"model":"fixture-model","max_tokens":16,"messages":[{"role":"user","content":"original"}]}`

func originalPrepared() *provider.MessagesRequest {
	return &provider.MessagesRequest{Body: []byte(originalBody), Path: "/v1/messages", Headers: http.Header{}}
}
func nativePeer(t *testing.T, write func(net.Conn, *http.Request)) (string, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	done := make(chan error, 1)
	go func() {
		peer, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer peer.Close()
		_ = peer.SetDeadline(time.Now().Add(3 * time.Second))
		request, err := http.ReadRequest(bufio.NewReader(peer))
		if err != nil {
			done <- err
			return
		}
		input, err := io.ReadAll(request.Body)
		if err != nil || string(input) != originalBody || request.URL.Path != "/v1/messages" {
			done <- errors.New("original body/path changed")
			return
		}
		write(peer, request)
		more, err := io.ReadAll(peer)
		if err == nil && len(more) > 0 {
			err = errors.New("duplicate physical request")
		}
		done <- err
	}()
	return "http://" + listener.Addr().String(), done
}
func joinedPeer(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("original peer not closed/joined")
	}
}
func nativeConfig(origin string) provider.AnthropicConfig {
	return provider.AnthropicConfig{APIKey: "fake-no-quota", BaseURL: origin, Timeout: time.Second, HTTPExchange: &provider.RawHTTP1Exchange{}}
}
func assertClosed(t *testing.T, body io.ReadCloser) {
	t.Helper()
	state, err := provider.InspectNativeOwnerBody(body)
	if err != nil || !state.Closed || !state.Joined || !state.OperationClosed || state.PendingReadiness {
		t.Fatalf("native lease not joined: %+v %v", state, err)
	}
}
func TestNativeBridgeRealPreparedEntryTransfersBodyAfterTimerClear(t *testing.T) {
	cleared := make(chan struct{})
	peerBodyWritten := make(chan struct{})
	origin, peer := nativePeer(t, func(conn net.Conn, _ *http.Request) {
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 4\r\nrequest-id: actual\r\n\r\n")
		<-cleared
		_, _ = io.WriteString(conn, "same")
		close(peerBodyWritten)
	})
	owner := cooperative.New()
	defer owner.Close()
	var body io.ReadCloser
	var kinds []string
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		driver, err := New(owner, scope, func(event provider.OwnerNativeEvent) {
			kinds = append(kinds, event.Kind)
			if event.Kind == "timer-clear" {
				close(cleared)
			}
		})
		if err != nil {
			return err
		}
		config := nativeConfig(origin)
		config.OwnerNativeIO = driver
		exchange, cancel, err := provider.NewAnthropic(config).OpenPreparedResponse(context.Background(), originalPrepared())
		if err != nil {
			return err
		}
		defer cancel()
		body = exchange.Response.Body
		before, err := provider.InspectNativeOwnerBody(body)
		if err != nil || before.Closed {
			return errors.New("short Await closed successful body lease")
		}
		if exchange.Response.StatusCode != 200 || exchange.OriginalHeaders == nil || exchange.Response.Header.Get("request-id") != "actual" {
			return errors.New("owner response projection changed")
		}
		raw, err := scope.AwaitBytes(cooperative.StreamNext, func(ctx context.Context) ([]byte, error) { return io.ReadAll(body) })
		if err != nil || string(raw) != "same" {
			return errors.New("body did not outlive fetch finally")
		}
		if err = body.Close(); err != nil {
			return err
		}
		return body.Close()
	})
	if err != nil {
		t.Fatal(err)
	}
	<-peerBodyWritten
	assertClosed(t, body)
	joinedPeer(t, peer)
	if strings.Join(kinds, ",") != "timer-arm,fetch-enter,fetch-fulfilled,timer-clear" {
		t.Fatal(kinds)
	}
}
func TestNativeBridgeCallerAbortAfterAdoptionClosesPhysicalBodyWithoutClosingOwner(t *testing.T) {
	origin, peer := nativePeer(t, func(conn net.Conn, _ *http.Request) {
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\n")
	})
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner := cooperative.New()
	defer owner.Close()
	var body io.ReadCloser
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		driver, err := New(owner, scope, nil)
		if err != nil {
			return err
		}
		config := nativeConfig(origin)
		config.OwnerNativeIO = driver
		exchange, stop, err := provider.NewAnthropic(config).OpenPreparedResponse(caller, originalPrepared())
		if err != nil {
			return err
		}
		defer stop()
		body = exchange.Response.Body
		cancel()
		_, err = scope.AwaitBytes(cooperative.StreamNext, func(context.Context) ([]byte, error) { return io.ReadAll(body) })
		_ = body.Close()
		if err == nil {
			return errors.New("caller body abort swallowed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertClosed(t, body)
	joinedPeer(t, peer)
	if err = owner.Run(context.Background(), func(*cooperative.Scope) error { return nil }); err != nil {
		t.Fatal("body cancellation closed original owner", err)
	}
}
func TestNativeBridgeHeaderFailuresCloseOriginalAndDupResources(t *testing.T) {
	cases := []struct {
		name, head string
		capability bool
	}{
		{"informational", "HTTP/1.1 103 Early Hints\r\n\r\n", true},
		{"upgrade", "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\n\r\n", true},
		{"redirect", "HTTP/1.1 302 Found\r\nLocation: /next\r\nContent-Length: 0\r\n\r\n", true},
		{"folded", "HTTP/1.1 200 OK\r\nX-Original: value\r\n continued\r\nContent-Length: 0\r\n\r\n", true},
		{"bad-status", "INVALID STATUS\r\nContent-Length: 0\r\n\r\n", false},
		{"eof-empty", "", false},
		{"eof-partial", "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n", false},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			origin, peer := nativePeer(t, func(conn net.Conn, _ *http.Request) {
				if item.head != "" {
					_, _ = io.WriteString(conn, item.head)
				}
				if strings.HasPrefix(item.name, "eof") {
					_ = conn.(*net.TCPConn).CloseWrite()
				}
			})
			owner := cooperative.New()
			defer owner.Close()
			var found error
			var cleared bool
			var state *provider.NativeOwnerResourceState
			err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				driver, err := New(owner, scope, func(e provider.OwnerNativeEvent) {
					if e.Kind == "timer-clear" {
						cleared = true
					}
					if e.Kind == "failed-lease-closed" {
						state = e.Resource
					}
				})
				if err != nil {
					return err
				}
				config := nativeConfig(origin)
				config.OwnerNativeIO = driver
				_, _, found = provider.NewAnthropic(config).OpenPreparedResponse(context.Background(), originalPrepared())
				return nil
			})
			if err != nil || found == nil || item.capability && !errors.Is(found, provider.ErrHTTPExchangeProfile) || !cleared {
				t.Fatalf("original header boundary: %v %v %v", err, found, cleared)
			}
			if state == nil || !state.Closed || !state.Joined || !state.OperationClosed || state.PendingReadiness {
				t.Fatalf("unadopted original resource not closed/joined: %+v", state)
			}
			joinedPeer(t, peer)
		})
	}
}
func TestNativeBridgeFiniteConfigurationGuardsHaveNoSocketFallback(t *testing.T) {
	cases := []struct {
		name string
		edit func(*provider.AnthropicConfig, *provider.MessagesRequest)
	}{
		{"pooled-custom-client", func(c *provider.AnthropicConfig, _ *provider.MessagesRequest) { c.HTTPClient = &http.Client{} }},
		{"missing-default-port", func(c *provider.AnthropicConfig, _ *provider.MessagesRequest) { c.BaseURL = "http://127.0.0.1" }},
		{"netHTTP-no-original-exchange", func(c *provider.AnthropicConfig, _ *provider.MessagesRequest) { c.HTTPExchange = nil }},
		{"custom-dial", func(c *provider.AnthropicConfig, _ *provider.MessagesRequest) {
			c.HTTPExchange = &provider.RawHTTP1Exchange{DialContext: func(context.Context, string, string) (net.Conn, error) {
				t.Fatal("custom dial reached")
				return nil, nil
			}}
		}},
		{"https", func(c *provider.AnthropicConfig, _ *provider.MessagesRequest) { c.BaseURL = "https://127.0.0.1:1" }},
		{"tls-config", func(c *provider.AnthropicConfig, _ *provider.MessagesRequest) {
			c.HTTPExchange = &provider.RawHTTP1Exchange{VerifiedTLS: &tls.Config{}}
		}},
		{"tls-resolver", func(c *provider.AnthropicConfig, _ *provider.MessagesRequest) {
			c.HTTPExchange = &provider.RawHTTP1Exchange{ResolveVerifiedTLS: func() (*tls.Config, error) { t.Fatal("TLS callback reached"); return nil, nil }}
		}},
		{"proxy", func(_ *provider.AnthropicConfig, p *provider.MessagesRequest) {
			p.Headers.Set("Proxy-Authorization", "fake")
		}},
		{"upgrade", func(_ *provider.AnthropicConfig, p *provider.MessagesRequest) { p.Headers.Set("Upgrade", "websocket") }},
		{"small-bound", func(c *provider.AnthropicConfig, _ *provider.MessagesRequest) {
			c.HTTPExchange = &provider.RawHTTP1Exchange{MaxHeaderBytes: 3}
		}},
		{"large-bound", func(c *provider.AnthropicConfig, _ *provider.MessagesRequest) {
			c.HTTPExchange = &provider.RawHTTP1Exchange{MaxHeaderBytes: 1024*1024 + 1}
		}},
		{"old-owner-adapter", func(c *provider.AnthropicConfig, _ *provider.MessagesRequest) {
			driver, _ := provider.NewOwnerOpenIO(func(provider.OwnerIOWait, func(context.Context) ([]byte, error)) ([]byte, error) {
				t.Fatal("old adapter reached")
				return nil, nil
			})
			c.OwnerOpenIO = driver
		}},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				driver, err := New(owner, scope, nil)
				if err != nil {
					return err
				}
				config := nativeConfig("http://127.0.0.1:1")
				config.OwnerNativeIO = driver
				p := originalPrepared()
				item.edit(&config, p)
				_, _, err = provider.NewAnthropic(config).OpenPreparedResponse(context.Background(), p)
				if !errors.Is(err, provider.ErrHTTPExchangeProfile) {
					return errors.New("implicit transport fallback or missing cap")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestNativeBridgeActualScopeAndTimerGuards(t *testing.T) {
	owner, foreign := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer foreign.Close()
	if _, err := New(owner, nil, nil); !provider.IsOwnerWaitFailure(err) {
		t.Fatal("nil scope is not original administrative failure", err)
	}
	if _, err := provider.NewOwnerNativeIO(nil, nil); !errors.Is(err, provider.ErrHTTPExchangeProfile) {
		t.Fatal(err)
	}
	var boundary *Boundary
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, err := New(foreign, scope, nil); !errors.Is(err, cooperative.ErrRestricted) {
			return errors.New("foreign owner scope admitted")
		}
		boundary = &Boundary{owner, scope}
		if _, err := boundary.ArmFetchTimer(time.Second, nil); !errors.Is(err, cooperative.ErrRestricted) {
			return err
		}
		if _, err := boundary.AwaitHeaders(nil); !errors.Is(err, cooperative.ErrRestricted) {
			return err
		}
		if !boundary.IsAdministrative(cooperative.ErrClosed) || !boundary.IsAdministrative(cooperative.ErrStop) || boundary.IsAdministrative(io.EOF) {
			return errors.New("administrative error class widened")
		}
		ticket, err := boundary.ArmFetchTimer(time.Hour, func() { t.Fatal("cleared callback ran") })
		if err != nil {
			return err
		}
		if err = ticket.Clear(); err != nil {
			return err
		}
		return ticket.Clear()
	})
	if err != nil {
		t.Fatal(err)
	}
	if boundary.CheckOwner() == nil {
		t.Fatal("expired scope retained")
	}
	if _, err = boundary.ArmFetchTimer(time.Second, func() {}); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
	if _, err = boundary.AwaitHeaders(nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
	if err := (&Boundary{}).CheckOwner(); err == nil {
		t.Fatal("empty bridge retained")
	}
}

func TestNativeBridgeOriginalTimerAndCallerAbortBeforeHeaders(t *testing.T) {
	for _, kind := range []string{"original-timer", "original-caller", "bounded-header"} {
		t.Run(kind, func(t *testing.T) {
			caller, cancel := context.WithCancel(context.Background())
			defer cancel()
			origin, peer := nativePeer(t, func(conn net.Conn, _ *http.Request) {
				if kind == "original-caller" {
					cancel()
				}
				if kind == "bounded-header" {
					_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nX-Original: partial")
				}
			})
			owner := cooperative.New()
			defer owner.Close()
			var found error
			var state *provider.NativeOwnerResourceState
			var fired, cleared bool
			err := owner.Run(caller, func(scope *cooperative.Scope) error {
				driver, err := New(owner, scope, func(e provider.OwnerNativeEvent) {
					if e.Kind == "timer-fire" {
						fired = true
					}
					if e.Kind == "timer-clear" {
						cleared = true
					}
					if e.Kind == "failed-lease-closed" {
						state = e.Resource
					}
				})
				if err != nil {
					return err
				}
				config := nativeConfig(origin)
				config.Timeout = 200 * time.Millisecond
				config.OwnerNativeIO = driver
				if kind == "bounded-header" {
					config.HTTPExchange = &provider.RawHTTP1Exchange{MaxHeaderBytes: 8}
				}
				_, _, found = provider.NewAnthropic(config).OpenPreparedResponse(caller, originalPrepared())
				return nil
			})
			if err != nil || !cleared || state == nil || !state.Closed || !state.Joined || !state.OperationClosed || state.PendingReadiness {
				t.Fatalf("native failed lease escaped: %v %+v %v", err, state, found)
			}
			if kind == "bounded-header" {
				if !errors.Is(found, provider.ErrHTTPExchangeProfile) {
					t.Fatal(found)
				}
			} else {
				var sdk *provider.SDKStreamError
				if !errors.As(found, &sdk) {
					t.Fatal(found)
				}
				class := "APIConnectionTimeoutError"
				if kind == "original-caller" {
					class = "APIUserAbortError"
				}
				if sdk.Class != class || fired != (kind == "original-timer") {
					t.Fatal("SDK cancellation owner changed", sdk, fired)
				}
			}
			joinedPeer(t, peer)
		})
	}
}
func TestNativeBridgeExpiredScopeRejectsBeforeFinalFetchEffects(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	var config provider.AnthropicConfig
	effects := 0
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		driver, err := New(owner, scope, nil)
		if err != nil {
			return err
		}
		config = nativeConfig("http://127.0.0.1:1")
		config.OwnerNativeIO = driver
		config.FinalizeHeaders = func(_ string, h http.Header) (http.Header, error) { effects++; return h, nil }
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, _, err := provider.NewAnthropic(config).OpenPreparedResponse(context.Background(), originalPrepared())
	if !provider.IsOwnerWaitFailure(err) || effects != 0 {
		t.Fatal("expired owner reached request effects or SDK error normalization", err, effects)
	}
}
func TestNativeBridgeCanceledOwnerArmKeepsAdministrativeError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner := cooperative.New()
	defer owner.Close()
	err := owner.Run(ctx, func(scope *cooperative.Scope) error {
		driver, err := New(owner, scope, nil)
		if err != nil {
			return err
		}
		config := nativeConfig("http://127.0.0.1:1")
		config.OwnerNativeIO = driver
		cancel()
		_, _, err = provider.NewAnthropic(config).OpenPreparedResponse(context.Background(), originalPrepared())
		if !provider.IsOwnerWaitFailure(err) || !errors.Is(err, context.Canceled) {
			return errors.New("original canceled owner became SDK fetch fault")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
