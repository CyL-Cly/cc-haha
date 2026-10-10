package provider

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestOwnerCloseWhilePhysicalOpenHeldClosesPeerBeforeReturn(t *testing.T) {
	accepted := make(chan struct{})
	peerClosed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		close(accepted)
		<-r.Context().Done()
		close(peerClosed)
	}))
	defer server.Close()
	owner := cooperative.New()
	defer owner.Close()
	var driver *OwnerOpenIO
	done := make(chan error, 1)
	go func() {
		done <- owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			driver = ownerTestDriver(t, scope)
			client := NewAnthropic(AnthropicConfig{BaseURL: server.URL, APIKey: "private-key", Timeout: time.Second, HTTPExchange: &RawHTTP1Exchange{}, OwnerOpenIO: driver})
			_, err := client.OpenMessagesStream(context.Background(), &MessagesRequest{Body: []byte(`{}`), Path: "/v1/messages?beta=true"})
			if !errors.Is(err, cooperative.ErrClosed) {
				return fmt.Errorf("scheduler failure reclassified: %v", err)
			}
			if driver.LeaseCount() != 0 {
				return errors.New("unadopted lease after owner Close")
			}
			return nil
		})
	}()
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("peer not accepted")
	}
	owner.Close()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, cooperative.ErrClosed) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("owner failed to join worker")
	}
	select {
	case <-peerClosed:
	case <-time.After(time.Second):
		t.Fatal("owner returned before peer closed")
	}
	if driver.LeaseCount() != 0 {
		t.Fatal("closed owner leaked body")
	}
}

func TestErrorBodyWaitFailureBelongsToOwnerNotSDKHTTPMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = io.WriteString(w, "actual failure")
	}))
	defer server.Close()
	failure := errors.New("owner error-body completion discarded")
	driver, _ := NewOwnerOpenIO(func(kind OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
		if kind == OwnerErrorBody {
			return nil, failure
		}
		return work(context.Background())
	})
	client := NewAnthropic(AnthropicConfig{BaseURL: server.URL, APIKey: "private-key", Timeout: time.Second, HTTPExchange: &RawHTTP1Exchange{}, OwnerOpenIO: driver})
	_, err := client.OpenMessagesStream(context.Background(), &MessagesRequest{Body: []byte(`{}`), Path: "/v1/messages?beta=true"})
	if err != failure || driver.LeaseCount() != 0 {
		t.Fatalf("owner scheduler failure was converted/swallowed: %v leases=%d", err, driver.LeaseCount())
	}
}

func TestSendPlanUnsupportedCapabilitiesNeverParkOrSend(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(*http.Request, *RawHTTP1Exchange)
	}{
		{"upgrade", func(r *http.Request, x *RawHTTP1Exchange) { r.Header.Set("Upgrade", "websocket") }},
		{"userinfo", func(r *http.Request, x *RawHTTP1Exchange) { r.URL.User = mustUserInfo() }},
		{"proxy-scheme", func(r *http.Request, x *RawHTTP1Exchange) { r.URL.Scheme = "socks5" }},
		{"custom-dial", func(r *http.Request, x *RawHTTP1Exchange) {
			x.DialContext = func(context.Context, string, string) (net.Conn, error) {
				t.Error("custom callback ran")
				return nil, nil
			}
		}},
		{"unbounded-headers", func(r *http.Request, x *RawHTTP1Exchange) { x.MaxHeaderBytes = 1 }},
		{"nonrepeatable-signedbody", func(r *http.Request, x *RawHTTP1Exchange) { r.GetBody = nil }},
		{"ambiguous-trust", func(r *http.Request, x *RawHTTP1Exchange) {
			r.URL.Scheme = "https"
			x.VerifiedTLS = &tls.Config{}
			x.ResolveVerifiedTLS = func() (*tls.Config, error) { t.Error("ambiguous TLS callback ran"); return nil, nil }
		}},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			r, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:1/", strings.NewReader("signed"))
			x := &RawHTTP1Exchange{}
			item.prepare(r, x)
			calls := 0
			driver, _ := NewOwnerOpenIO(func(OwnerIOWait, func(context.Context) ([]byte, error)) ([]byte, error) { calls++; return nil, nil })
			_, err := driver.open(r, x, nil)
			if !errors.Is(err, ErrHTTPExchangeProfile) || calls != 0 || driver.LeaseCount() != 0 {
				t.Fatalf("unsupported plan=%v calls=%d leases=%d", err, calls, driver.LeaseCount())
			}
		})
	}
}

func mustUserInfo() *url.Userinfo { return url.User("private-user") }

// The physical worker reports original bytes. Unsupported native HTTP/1
// responses close their lease; they do not become a successful SDK stream.
func TestOwnerResponseCapabilitiesCloseUnadoptedSocket(t *testing.T) {
	cases := []struct {
		name, reply string
		limit       int
	}{
		{"redirect", "HTTP/1.1 302 Found\r\nLocation: /elsewhere\r\nContent-Length: 0\r\n\r\n", 0},
		{"folded", "HTTP/1.1 200 OK\r\nX-Original: first\r\n last\r\nContent-Length: 0\r\n\r\n", 0},
		{"capture-bound", "HTTP/1.1 200 OK\r\nX-Long: abcdefghijklmnopqrstuvwxyz\r\nContent-Length: 0\r\n\r\n", 24},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			closed := make(chan struct{})
			go func() {
				c, e := listener.Accept()
				if e != nil {
					return
				}
				defer c.Close()
				r, e := http.ReadRequest(bufio.NewReader(c))
				if e != nil {
					return
				}
				_, _ = io.Copy(io.Discard, r.Body)
				_ = r.Body.Close()
				_, _ = io.WriteString(c, item.reply)
				_, _ = io.Copy(io.Discard, c)
				close(closed)
			}()
			driver, _ := NewOwnerOpenIO(func(_ OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
				return work(context.Background())
			})
			r, _ := http.NewRequest(http.MethodPost, "http://"+listener.Addr().String(), strings.NewReader("signed"))
			_, err = driver.open(r, &RawHTTP1Exchange{MaxHeaderBytes: item.limit}, nil)
			if !errors.Is(err, ErrHTTPExchangeProfile) || driver.LeaseCount() != 0 {
				t.Fatalf("unsupported response %v lease %d", err, driver.LeaseCount())
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("unadopted response socket remained open")
			}
		})
	}
}

func TestOwnerRejectsCorruptCompletionSnapshotWithoutLeaking(t *testing.T) {
	for _, raw := range []string{"invalid-json", `{"status":200,"headers":""}`, `{"status":200,"headers":"SFRUUC8xLjEgMjAwIE9LDQpYLUJhZA0KDQo="}`} {
		t.Run(raw, func(t *testing.T) {
			driver, _ := NewOwnerOpenIO(func(OwnerIOWait, func(context.Context) ([]byte, error)) ([]byte, error) { return []byte(raw), nil })
			r, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:1/", strings.NewReader("signed"))
			_, err := driver.open(r, &RawHTTP1Exchange{}, nil)
			if err == nil || driver.LeaseCount() != 0 {
				t.Fatalf("corrupt immutable snapshot=%v leases=%d", err, driver.LeaseCount())
			}
		})
	}
}

func TestOwnerCloseJoinAndLateResourceRace(t *testing.T) {
	driver, _ := NewOwnerOpenIO(func(OwnerIOWait, func(context.Context) ([]byte, error)) ([]byte, error) { return nil, nil })
	lease := driver.reserve(context.Background(), nil)
	lease.closePhysical()
	a, b := net.Pipe()
	defer b.Close()
	if lease.setConnection(a) {
		t.Fatal("closed lease accepted late resource")
	}
	if lease.begin() {
		t.Fatal("closed lease accepted new worker")
	}
	data := make([]byte, 1)
	if n, err := lease.Read(data); n != 0 || err != io.EOF {
		t.Fatal(n, err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if driver.LeaseCount() != 0 {
		t.Fatal("double close leaked reserved lease")
	}
}

func TestOwnerUsesNativeBodyFailureWithoutCompletionReclassification(t *testing.T) {
	driver, _ := NewOwnerOpenIO(func(_ OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
		return work(context.Background())
	})
	lease := driver.reserve(context.Background(), nil)
	lease.closePhysical()
	defer lease.Close()
	_, err := driver.errorBody(lease)
	var completion *ownerWaitFailure
	if !errors.Is(err, context.Canceled) || errors.As(err, &completion) {
		t.Fatalf("native body failure=%v", err)
	}
}

func TestOwnerPhysicalFaultFactsDoNotReadOwnerCallbacks(t *testing.T) {
	driver, _ := NewOwnerOpenIO(func(OwnerIOWait, func(context.Context) ([]byte, error)) ([]byte, error) { return nil, nil })
	for _, endpoint := range []string{"http://[", "http://127.0.0.1:1/"} {
		lease := driver.reserve(context.Background(), nil)
		_, err := physicalOpen(context.Background(), lease, &SendPlan{endpoint: endpoint, method: "POST", headers: http.Header{}, headerLimit: 65536})
		var native *ownerNativeFault
		if !errors.As(err, &native) {
			t.Fatal(err)
		}
		if native.Error() != native.cause.Error() {
			t.Fatal("native diagnostic changed")
		}
		_ = lease.Close()
	}
	closed := driver.reserve(context.Background(), nil)
	closed.closePhysical()
	defer closed.Close()
	if _, err := physicalOpen(context.Background(), closed, &SendPlan{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type ownerBodyReadFailure struct{ fault error }

func (body ownerBodyReadFailure) Read([]byte) (int, error) { return 0, body.fault }
func (body ownerBodyReadFailure) Close() error             { return nil }

func TestSignedBodySnapshotFailureCannotParkOrLeak(t *testing.T) {
	failure := errors.New("original signed body snapshot failed")
	for _, readerFailure := range []bool{false, true} {
		t.Run(fmt.Sprint(readerFailure), func(t *testing.T) {
			r, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:1/", strings.NewReader("signed"))
			r.GetBody = func() (io.ReadCloser, error) {
				if readerFailure {
					return ownerBodyReadFailure{failure}, nil
				}
				return nil, failure
			}
			driver, _ := NewOwnerOpenIO(func(OwnerIOWait, func(context.Context) ([]byte, error)) ([]byte, error) {
				t.Error("failed owner snapshot parked physical IO")
				return nil, nil
			})
			_, err := driver.open(r, &RawHTTP1Exchange{}, nil)
			if !errors.Is(err, failure) || driver.LeaseCount() != 0 {
				t.Fatalf("body original failure %v leases=%d", err, driver.LeaseCount())
			}
		})
	}
}

func TestUnverifiedTLSConfigRejectedOnOwnerBeforePhysicalAwait(t *testing.T) {
	driver, _ := NewOwnerOpenIO(func(OwnerIOWait, func(context.Context) ([]byte, error)) ([]byte, error) {
		t.Error("TLS business guard ran in worker")
		return nil, nil
	})
	r, _ := http.NewRequest(http.MethodPost, "https://127.0.0.1:1/", strings.NewReader("signed"))
	_, err := driver.open(r, &RawHTTP1Exchange{VerifiedTLS: &tls.Config{InsecureSkipVerify: true}}, nil)
	if !errors.Is(err, ErrHTTPExchangeProfile) || driver.LeaseCount() != 0 {
		t.Fatal(err, driver.LeaseCount())
	}
}

func TestHeldPhysicalTLSOpenPumpsReadOnOriginalOwner(t *testing.T) {
	release := make(chan struct{})
	accepted := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		close(accepted)
		select {
		case <-release:
			_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	owner := cooperative.New()
	defer owner.Close()
	done := make(chan error, 1)
	trace := []string{} // exclusively written on the owner, checked after Run returns
	go func() {
		done <- owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			driver := ownerTestDriver(t, scope)
			client := NewAnthropic(AnthropicConfig{BaseURL: server.URL, APIKey: "private-key", Timeout: time.Second, OwnerOpenIO: driver, HTTPExchange: &RawHTTP1Exchange{VerifiedTLS: &tls.Config{RootCAs: pool}}, FinalizeHeaders: func(_ string, h http.Header) (http.Header, error) {
				err := scope.Inline(cooperative.Read, func(*cooperative.Scope) error { trace = append(trace, "finalize"); return nil })
				return h, err
			}})
			stream, err := client.OpenMessagesStream(context.Background(), &MessagesRequest{Body: []byte(`{}`), Path: "/v1/messages?beta=true"})
			if err != nil {
				return err
			}
			defer stream.Close()
			trace = append(trace, "adopt")
			if _, err = stream.Next(); err != nil {
				return err
			}
			return nil
		})
	}()
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("TLS peer not accepted")
	}
	receipt, err := owner.Submit(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error {
		trace = append(trace, "read")
		if err := scope.Frame(cooperative.FrameFeed, func() error { t.Error("nested Controller frame executed"); return nil }); !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("held IO allowed concurrent frame: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = receipt.Wait(); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("physical TLS failed to resume owner")
	}
	if !reflect.DeepEqual(trace, []string{"finalize", "read", "adopt"}) {
		t.Fatal(trace)
	}
}
