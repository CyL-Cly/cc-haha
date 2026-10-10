package provider

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

func privateTLSCertificate(t *testing.T, ip string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "private-native-fixture"},
		NotBefore: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC),
		BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		IPAddresses: []net.IP{net.ParseIP(ip)}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(certificate)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: certificate}, pool
}

func privateTLSServer(t *testing.T, certificate tls.Certificate, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = true // The explicit raw transport must still negotiate H1.
	server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, NextProtos: []string{"h2", "http/1.1"}}
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	t.Cleanup(func() { server.CloseClientConnections(); server.Close() })
	return server
}

func TestRawVerifiedTLSPreservesMetadataReaderAndSinglePhysicalSend(t *testing.T) {
	certificate, roots := privateTLSCertificate(t, "127.0.0.1")
	var requests atomic.Int32
	server := privateTLSServer(t, certificate, func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.TLS.NegotiatedProtocol != "http/1.1" || request.Proto != "HTTP/1.1" {
			t.Errorf("unported negotiated protocol: %s %s", request.TLS.NegotiatedProtocol, request.Proto)
		}
		payload, _ := io.ReadAll(request.Body)
		if string(payload) != `{"original":true}` {
			t.Errorf("TLS altered prepared request bytes: %s", payload)
		}
		connection, _, err := writer.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.Close()
		_, _ = io.WriteString(connection, "HTTP/1.1 200 Fixture\r\nX-Future: first\r\nSet-Cookie: a=1\r\nX-Future: second\r\nSet-Cookie: b=2\r\nContent-Length: 4\r\nConnection: close\r\n\r\nbody")
	})
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/messages", strings.NewReader(`{"original":true}`))
	config := &tls.Config{RootCAs: roots}
	result, err := (&RawHTTP1Exchange{VerifiedTLS: config}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Response.Body.Close()
	body, err := io.ReadAll(result.Response.Body)
	if err != nil || string(body) != "body" || requests.Load() != 1 {
		t.Fatalf("reader/send ownership: %q %v sends%d", body, err, requests.Load())
	}
	if value, found, err := result.OriginalHeaders.Get("x-future"); err != nil || !found || value != "second" {
		t.Fatalf("original duplicate metadata was lost: %q %v", value, err)
	}
	if !reflect.DeepEqual(result.OriginalHeaders.SetCookies(), []string{"a=1", "b=2"}) {
		t.Fatal("TLS collapsed independent original cookie rows")
	}
	if value, found, err := result.OriginalHeaders.Get("connection"); err != nil || !found || value != "close" {
		t.Fatal("original close header was inferred or discarded", value, err)
	}
	if config.ServerName != "" || config.NextProtos != nil || config.MinVersion != 0 {
		t.Fatal("request mutated process-owned trust configuration")
	}
}

func TestRawVerifiedTLSKeepsObservedNativeCertificateFailuresBeforePOST(t *testing.T) {
	for _, mode := range []string{"untrusted-self-signed", "trusted-wrong-ip"} {
		t.Run(mode, func(t *testing.T) {
			ip := "127.0.0.1"
			if mode == "trusted-wrong-ip" {
				ip = "127.0.0.2"
			}
			certificate, roots := privateTLSCertificate(t, ip)
			if mode == "untrusted-self-signed" {
				roots = x509.NewCertPool()
			}
			var requests atomic.Int32
			server := privateTLSServer(t, certificate, func(http.ResponseWriter, *http.Request) { requests.Add(1) })
			request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/messages", strings.NewReader(`{}`))
			_, err := (&RawHTTP1Exchange{VerifiedTLS: &tls.Config{RootCAs: roots}}).Do(request)
			var native *TLSConnectionFault
			if !errors.As(err, &native) || requests.Load() != 0 {
				t.Fatalf("verification failure reached model or lost native record: %v sends%d", err, requests.Load())
			}
			code, message := "DEPTH_ZERO_SELF_SIGNED_CERT", "self signed certificate"
			if mode == "trusted-wrong-ip" {
				code = "ERR_TLS_CERT_ALTNAME_INVALID"
				message = `ERR_TLS_CERT_ALTNAME_INVALID fetching "` + request.URL.String() + `". For more information, pass ` + "`verbose: true`" + ` in the second argument to fetch()`
			}
			if native.Code() != code || native.Error() != message || native.verification == nil || errors.Unwrap(native) != nil {
				t.Fatalf("native fields or Source's absent cause changed: %s %q", native.Code(), native.Error())
			}
			properties, err := native.EnumerableErrorJSON()
			raw, _ := properties.MarshalJSON()
			wanted := `{"code":"` + code + `","path":"` + request.URL.String() + `","errno":0}`
			if err != nil || string(raw) != wanted {
				t.Fatalf("Source native enumerable order/value changed: %s %v", raw, err)
			}
		})
	}
}

func TestRawVerifiedTLSRejectsUnportedConfigurationBeforeDial(t *testing.T) {
	mutations := map[string]func(*tls.Config){
		"system defaults":      func(config *tls.Config) { config.RootCAs = nil },
		"insecure":             func(config *tls.Config) { config.InsecureSkipVerify = true },
		"replace verification": func(config *tls.Config) { config.VerifyConnection = func(tls.ConnectionState) error { return nil } },
		"replace peer verification": func(config *tls.Config) {
			config.VerifyPeerCertificate = func([][]byte, [][]*x509.Certificate) error { return nil }
		},
		"mTLS certificate": func(config *tls.Config) { config.Certificates = []tls.Certificate{{}} },
		"mTLS getter": func(config *tls.Config) {
			config.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return nil, nil }
		},
		"session cache":        func(config *tls.Config) { config.ClientSessionCache = tls.NewLRUClientSessionCache(1) },
		"different SNI":        func(config *tls.Config) { config.ServerName = "different.invalid" },
		"HTTP2":                func(config *tls.Config) { config.NextProtos = []string{"h2"} },
		"mixed ALPN":           func(config *tls.Config) { config.NextProtos = []string{"http/1.1", "h2"} },
		"old TLS":              func(config *tls.Config) { config.MinVersion = tls.VersionTLS11 },
		"max version override": func(config *tls.Config) { config.MaxVersion = tls.VersionTLS13 },
		"cipher override":      func(config *tls.Config) { config.CipherSuites = []uint16{tls.TLS_AES_128_GCM_SHA256} },
		"curve override":       func(config *tls.Config) { config.CurvePreferences = []tls.CurveID{tls.CurveP256} },
		"renegotiation":        func(config *tls.Config) { config.Renegotiation = tls.RenegotiateOnceAsClient },
		"key logger":           func(config *tls.Config) { config.KeyLogWriter = io.Discard },
		"ECH":                  func(config *tls.Config) { config.EncryptedClientHelloConfigList = []byte{1} },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			config := &tls.Config{RootCAs: x509.NewCertPool()}
			mutate(config)
			request, _ := http.NewRequest(http.MethodPost, "https://127.0.0.1/v1/messages", nil)
			transport := &RawHTTP1Exchange{VerifiedTLS: config, DialContext: func(context.Context, string, string) (net.Conn, error) {
				t.Fatal("unported TLS profile dialed a target")
				return nil, nil
			}}
			if _, err := transport.Do(request); !errors.Is(err, ErrHTTPExchangeProfile) {
				t.Fatal(err)
			}
		})
	}
}

func TestTLSConfigurationAndFaultCapabilityBoundaries(t *testing.T) {
	roots := x509.NewCertPool()
	input := &tls.Config{RootCAs: roots, ServerName: "127.0.0.1", NextProtos: []string{"http/1.1"}, MinVersion: tls.VersionTLS12}
	copy, err := verifiedHTTP1TLS(input, "127.0.0.1")
	if err != nil || copy == input || copy.RootCAs == input.RootCAs || !reflect.DeepEqual(copy.NextProtos, input.NextProtos) {
		t.Fatal("process trust state was not independently snapshotted", copy, err)
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		if originalTLSFault(cause, "https://fixture") != cause {
			t.Fatal("native classifier replaced abort/timeout ownership")
		}
	}
	if err := originalTLSFault(errors.New("unobserved alert"), "https://fixture"); !errors.Is(err, ErrHTTPExchangeProfile) {
		t.Fatal("unported native fault was disguised as a Source certificate error", err)
	}
}

func TestRawVerifiedTLSHandshakeCancellationClosesWithoutPOST(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted, ended := make(chan struct{}), make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			ended <- err
			return
		}
		defer connection.Close()
		close(accepted)
		_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, err = io.Copy(io.Discard, connection)
		ended <- err
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+listener.Addr().String()+"/v1/messages", strings.NewReader("must not send"))
	go func() { <-accepted; cancel() }()
	_, err = (&RawHTTP1Exchange{VerifiedTLS: &tls.Config{RootCAs: x509.NewCertPool()}}).Do(request)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("handshake cancellation changed caller cause", err)
	}
	select {
	case err := <-ended:
		if err != nil {
			t.Fatal("canceled TLS socket did not close before fixture deadline", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled handshake left an open connection")
	}
}

func TestRawDeferredTLSParsesAtFetchAndRejectsAmbiguousTrustBeforeDial(t *testing.T) {
	request, _ := http.NewRequest(http.MethodPost, "https://127.0.0.1/v1/messages?beta=true", nil)
	for _, mode := range []string{"malformed", "unported", "ambiguous", "nil config"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			exchange := &RawHTTP1Exchange{
				DialContext: func(context.Context, string, string) (net.Conn, error) {
					t.Fatal("invalid original CA configuration opened a socket")
					return nil, nil
				},
				ResolveVerifiedTLS: func() (*tls.Config, error) {
					calls++
					if mode == "malformed" {
						return nil, &tlsconfig.InvalidCertificateError{Index: 145}
					}
					if mode == "unported" {
						return nil, &tlsconfig.CapabilityError{Boundary: "original PEM extension"}
					}
					return nil, nil
				},
			}
			if mode == "ambiguous" {
				exchange.VerifiedTLS = &tls.Config{RootCAs: x509.NewCertPool()}
			}
			_, err := exchange.Do(request)
			if mode == "malformed" {
				var native *TLSConnectionFault
				if !errors.As(err, &native) || native.Code() != "FailedToOpenSocket" || native.Error() != "Was there a typo in the url or port?" || errors.Unwrap(native) != nil {
					t.Fatalf("malformed CA lost original native record: %v", err)
				}
				own, _ := native.EnumerableErrorJSON()
				raw, _ := own.MarshalJSON()
				if string(raw) != `{"code":"FailedToOpenSocket","path":"https://127.0.0.1/v1/messages?beta=true","errno":0}` {
					t.Fatal("malformed CA native own-property record changed", string(raw))
				}
			} else if !errors.Is(err, ErrHTTPExchangeProfile) {
				t.Fatal("unported TLS became an ordinary connection failure", err)
			}
			wanted := 1
			if mode == "ambiguous" {
				wanted = 0
			}
			if calls != wanted {
				t.Fatal("fetch-stage trust resolver call count changed", calls)
			}
		})
	}
}

func TestRawVerifiedTLSUsesOriginalHTTPSDefaultPort(t *testing.T) {
	request, _ := http.NewRequest(http.MethodPost, "https://127.0.0.1/v1/messages", nil)
	sentinel := errors.New("private dial observation")
	dials := 0
	_, err := (&RawHTTP1Exchange{VerifiedTLS: &tls.Config{RootCAs: x509.NewCertPool()},
		DialContext: func(_ context.Context, network, address string) (net.Conn, error) {
			dials++
			if network != "tcp" || address != "127.0.0.1:443" {
				t.Fatalf("original HTTPS default address changed: %s %s", network, address)
			}
			return nil, sentinel
		}}).Do(request)
	if err != sentinel || dials != 1 {
		t.Fatal("dial failure retried or changed ownership", err, dials)
	}
}

func TestVerifiedTLSSDKFetchTimeoutStopsAtHeadersBeforeLateSSE(t *testing.T) {
	certificate, roots := privateTLSCertificate(t, "127.0.0.1")
	var requests atomic.Int32
	server := privateTLSServer(t, certificate, func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Set("Request-Id", "late-tls-body")
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		select {
		case <-time.After(1250 * time.Millisecond):
			_, _ = io.WriteString(writer, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		case <-request.Context().Done():
		}
	})
	client := NewAnthropic(AnthropicConfig{BaseURL: server.URL, APIKey: "fake", Timeout: time.Second,
		HTTPExchange: &RawHTTP1Exchange{VerifiedTLS: &tls.Config{RootCAs: roots}}})
	stream, err := client.OpenMessagesStream(context.Background(), preparedStream())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	part, err := stream.Next()
	if err != nil || string(part) != `{"type":"message_stop"}` || stream.RequestID == nil || *stream.RequestID != "late-tls-body" {
		t.Fatal("SDK fetch timer leaked past headers into TLS body", string(part), err)
	}
	if _, err := stream.Next(); err != io.EOF || stream.AbortCause() != nil || requests.Load() != 1 {
		t.Fatal("late TLS stream duplicated sends or became aborted", err, stream.AbortCause(), requests.Load())
	}
}

func TestVerifiedTLSSDKCallerAbortAfterHeadersQuietlyClosesConnection(t *testing.T) {
	certificate, roots := privateTLSCertificate(t, "127.0.0.1")
	var requests atomic.Int32
	ended := make(chan struct{})
	server := privateTLSServer(t, certificate, func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		<-request.Context().Done()
		close(ended)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := NewAnthropic(AnthropicConfig{BaseURL: server.URL, APIKey: "fake", Timeout: time.Second,
		HTTPExchange: &RawHTTP1Exchange{VerifiedTLS: &tls.Config{RootCAs: roots}}})
	stream, err := client.OpenMessagesStream(ctx, preparedStream())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	cancel()
	if _, err := stream.Next(); err != io.EOF || !errors.Is(stream.AbortCause(), context.Canceled) {
		t.Fatal("TLS body abort lost SDK quiet-EOF ownership", err, stream.AbortCause())
	}
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Fatal("caller abort left physical TLS socket open")
	}
	if requests.Load() != 1 {
		t.Fatal("body cancellation retried physical TLS send", requests.Load())
	}
}

func TestVerifiedTLSSDKHandshakeTimerIsDistinctFromOriginalCallerAbort(t *testing.T) {
	for _, mode := range []string{"SDK timer", "caller deadline"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			ended := make(chan error, 1)
			go func() {
				connection, err := listener.Accept()
				if err != nil {
					ended <- err
					return
				}
				defer connection.Close()
				_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
				_, err = io.Copy(io.Discard, connection)
				ended <- err
			}()
			ctx := context.Background()
			timeout := 100 * time.Millisecond
			if mode == "caller deadline" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
				timeout = 2 * time.Second
			}
			client := NewAnthropic(AnthropicConfig{BaseURL: "https://" + listener.Addr().String(), APIKey: "fake", Timeout: timeout,
				HTTPExchange: &RawHTTP1Exchange{VerifiedTLS: &tls.Config{RootCAs: x509.NewCertPool()}}})
			_, err = client.OpenMessagesStream(ctx, preparedStream())
			var sdk *SDKStreamError
			wanted := "APIConnectionTimeoutError"
			if mode == "caller deadline" {
				wanted = "APIUserAbortError"
			}
			if !errors.As(err, &sdk) || sdk.Class != wanted || sdk.Status != nil || sdk.Cause != nil {
				t.Fatalf("TLS handshake changed SDK timer/caller signal distinction: %v", err)
			}
			select {
			case err := <-ended:
				if err != nil {
					t.Fatal("TLS handshake timer left physical socket open", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("TLS handshake timer did not release socket")
			}
		})
	}
}
