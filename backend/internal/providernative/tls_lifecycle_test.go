package providernative

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

func tlsLifecycleTrust(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	certificate, err := tls.LoadX509KeyPair("../../../scripts/quality-gate/go-parity/testdata/private-loopback-certificate.pem", "../../../scripts/quality-gate/go-parity/testdata/private-loopback-key.pem")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../../scripts/quality-gate/go-parity/testdata/private-loopback-certificate.pem")
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(raw) {
		t.Fatal("public fixture root")
	}
	return certificate, roots
}
func TestNativeTLSBodyHeldUntilOwnerFinallyKeepsOneJoinedLease(t *testing.T) {
	certificate, roots := tlsLifecycleTrust(t)
	cleared := make(chan struct{})
	peerClosed := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload, _ := io.ReadAll(request.Body)
		if string(payload) != originalBody {
			t.Error("prepared TLS body changed")
		}
		writer.Header().Set("Content-Length", "4")
		writer.WriteHeader(200)
		writer.(http.Flusher).Flush()
		<-cleared
		io.WriteString(writer, "same")
		writer.(http.Flusher).Flush()
		<-request.Context().Done()
		close(peerClosed)
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, NextProtos: []string{"http/1.1"}}
	server.StartTLS()
	defer server.Close()
	defer server.CloseClientConnections()
	owner := cooperative.New()
	defer owner.Close()
	var body io.ReadCloser
	var phases []string
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		bridge, err := New(owner, scope, func(event provider.OwnerNativeEvent) {
			phases = append(phases, event.Kind)
			if event.Kind == "timer-clear" {
				close(cleared)
			}
		})
		if err != nil {
			return err
		}
		config := nativeConfig(server.URL)
		config.OwnerNativeIO = bridge
		config.HTTPExchange = &provider.RawHTTP1Exchange{VerifiedTLS: &tls.Config{RootCAs: roots}}
		exchange, stop, err := provider.NewAnthropic(config).OpenPreparedResponse(context.Background(), originalPrepared())
		if err != nil {
			return err
		}
		defer stop()
		body = exchange.Response.Body
		before, err := provider.InspectNativeOwnerBody(body)
		if err != nil || before.Closed {
			return errors.New("temporary await consumed TLS lease")
		}
		result, err := scope.AwaitBytes(cooperative.StreamNext, func(context.Context) ([]byte, error) { return io.ReadAll(body) })
		if err != nil {
			return err
		}
		if string(result) != "same" {
			return errors.New("TLS record read lost bytes")
		}
		return body.Close()
	})
	if err != nil {
		t.Fatal(err)
	}
	assertClosed(t, body)
	select {
	case <-peerClosed:
	case <-time.After(3 * time.Second):
		t.Fatal("original TLS peer remained open")
	}
	if strings.Join(phases, ",") != "timer-arm,fetch-enter,fetch-fulfilled,timer-clear" {
		t.Fatal(phases)
	}
}
func TestNativeTLSHandshakeCallerAbortClosesOriginalAndDuplicateWithoutStoppingOwner(t *testing.T) {
	_, roots := tlsLifecycleTrust(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	peer := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			peer <- err
			return
		}
		defer conn.Close()
		one := make([]byte, 1)
		_, err = io.ReadFull(conn, one)
		if err != nil {
			peer <- err
			return
		}
		cancel()
		_, err = io.Copy(io.Discard, conn)
		peer <- err
	}()
	owner := cooperative.New()
	defer owner.Close()
	var failedState *provider.NativeOwnerResourceState
	err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		bridge, err := New(owner, scope, func(event provider.OwnerNativeEvent) {
			if event.Resource != nil {
				state := *event.Resource
				failedState = &state
			}
		})
		if err != nil {
			return err
		}
		config := nativeConfig("https://" + listener.Addr().String())
		config.OwnerNativeIO = bridge
		config.HTTPExchange = &provider.RawHTTP1Exchange{VerifiedTLS: &tls.Config{RootCAs: roots}}
		_, _, err = provider.NewAnthropic(config).OpenPreparedResponse(caller, originalPrepared())
		if err == nil || err.Error() != "Request was aborted." {
			return errors.New("handshake caller cancel changed error ownership")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	joinedPeer(t, peer)
	if failedState == nil || !failedState.Closed || !failedState.Joined || failedState.OperationClosed || failedState.PendingReadiness {
		t.Fatal("cancelled pre-publication handshake resource not joined or invented an adopted operation", failedState)
	}
	if err = owner.Run(context.Background(), func(*cooperative.Scope) error { return nil }); err != nil {
		t.Fatal("request abort closed owner", err)
	}
}
func TestNativeTLSUnsupportedTrustIsRejectedAfterTimerArmWithoutDial(t *testing.T) {
	_, roots := tlsLifecycleTrust(t)
	for _, mode := range []string{"ambiguous", "custom verifier", "missing roots", "unported trust resolver failure"} {
		t.Run(mode, func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			var phases []string
			err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				bridge, err := New(owner, scope, func(event provider.OwnerNativeEvent) { phases = append(phases, event.Kind) })
				if err != nil {
					return err
				}
				trust := &tls.Config{RootCAs: roots}
				exchange := &provider.RawHTTP1Exchange{VerifiedTLS: trust}
				switch mode {
				case "ambiguous":
					exchange.ResolveVerifiedTLS = func() (*tls.Config, error) { t.Fatal("ambiguous trust ran"); return nil, nil }
				case "custom verifier":
					trust.InsecureSkipVerify = true
				case "missing roots":
					exchange.VerifiedTLS = nil
				case "unported trust resolver failure":
					exchange.VerifiedTLS = nil
					exchange.ResolveVerifiedTLS = func() (*tls.Config, error) { return nil, errors.New("outside original captured CA parse domain") }
				}
				config := nativeConfig("https://127.0.0.1:1")
				config.OwnerNativeIO = bridge
				config.HTTPExchange = exchange
				_, _, err = provider.NewAnthropic(config).OpenPreparedResponse(context.Background(), originalPrepared())
				if !errors.Is(err, provider.ErrHTTPExchangeProfile) {
					return errors.New("unsupported TLS trust was accepted")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(phases, ",") != "timer-arm,timer-clear" {
				t.Fatal("trust parse moved before armed timer", phases)
			}
		})
	}
}

func TestNativeTLSUnportedHandshakeProtocolStaysCapabilityAndJoinsPeer(t *testing.T) {
	_, roots := tlsLifecycleTrust(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	peerDone := make(chan error, 1)
	go func() {
		peer, err := listener.Accept()
		if err != nil {
			peerDone <- err
			return
		}
		defer peer.Close()
		first := make([]byte, 1)
		if _, err = io.ReadFull(peer, first); err != nil {
			peerDone <- err
			return
		}
		_, err = io.WriteString(peer, "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
		if err == nil {
			_, err = io.Copy(io.Discard, peer)
		}
		peerDone <- err
	}()
	owner := cooperative.New()
	defer owner.Close()
	var state *provider.NativeOwnerResourceState
	err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		bridge, err := New(owner, scope, func(event provider.OwnerNativeEvent) {
			if event.Resource != nil {
				value := *event.Resource
				state = &value
			}
		})
		if err != nil {
			return err
		}
		config := nativeConfig("https://" + listener.Addr().String())
		config.OwnerNativeIO = bridge
		config.HTTPExchange = &provider.RawHTTP1Exchange{VerifiedTLS: &tls.Config{RootCAs: roots}}
		_, _, err = provider.NewAnthropic(config).OpenPreparedResponse(context.Background(), originalPrepared())
		if !errors.Is(err, provider.ErrHTTPExchangeProfile) {
			return errors.New("unproven plaintext TLS peer was silently normalized to SDK timeout/connection")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	joinedPeer(t, peerDone)
	if state == nil || !state.Closed || !state.Joined || state.PendingReadiness {
		t.Fatal("unported TLS fault leaked original/duplicate transport", state)
	}
}
