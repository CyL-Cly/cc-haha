package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

type readStartObserver struct {
	io.ReadCloser
	entered chan struct{}
	once    sync.Once
}

func (body *readStartObserver) Read(out []byte) (int, error) {
	body.once.Do(func() { close(body.entered) })
	return body.ReadCloser.Read(out)
}

func readAwait(scope *cooperative.Scope, kind OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
	wait := cooperative.NetworkOpen
	if int(kind) == 2 {
		wait = cooperative.StreamNext
	}
	return scope.AwaitBytes(wait, work)
}
func soleReadLease(driver *OwnerOpenIO) *ownerLease {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	for _, lease := range driver.leases {
		return lease
	}
	return nil
}
func TestOwnerReadPumpsOriginalCancel(t *testing.T) {
	peerClosed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(peerClosed)
	}))
	defer server.Close()
	owner := cooperative.New()
	defer owner.Close()
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			driver, _ := NewOwnerOpenIO(func(kind OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
				return readAwait(scope, kind, work)
			})
			client := NewAnthropic(AnthropicConfig{BaseURL: server.URL, APIKey: "private-key", Timeout: 3 * time.Second, HTTPExchange: &RawHTTP1Exchange{}, OwnerOpenIO: driver})
			stream, err := client.OpenMessagesStream(caller, &MessagesRequest{Body: []byte(`{}`), Path: "/v1/messages?beta=true"})
			if err != nil {
				return err
			}
			defer stream.Close()
			lease := soleReadLease(driver)
			lease.body = &readStartObserver{ReadCloser: lease.body, entered: entered}
			_, err = stream.Next()
			if !errors.Is(err, io.EOF) || !errors.Is(stream.AbortCause(), context.Canceled) {
				return fmt.Errorf("caller abort %v/%v", err, stream.AbortCause())
			}
			return nil
		})
	}()
	select {
	case <-entered:
	case early := <-done:
		t.Fatalf("open returned before actual read: %v", early)
	case <-time.After(2 * time.Second):
		cancel()
		<-done
		t.Fatal("actual read did not start")
	}
	receipt, err := owner.Submit(context.Background(), cooperative.Cancel, func(*cooperative.Scope) error { cancel(); return nil })
	if err != nil {
		t.Fatal(err)
	}
	red := false
	select {
	case <-receipt.Done():
		if err = receipt.Wait(); err != nil {
			t.Fatal(err)
		}
	case <-time.After(150 * time.Millisecond):
		red = true
		cancel()
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-peerClosed:
	case <-time.After(time.Second):
		t.Fatal("actual peer did not close")
	}
	if red {
		t.Fatal("pending physical Read prevented the original Cancel callback")
	}
}

func TestOwnerDemandReadDrainsExistingBufferBeforeAnotherAwait(t *testing.T) {
	wire := "event: message_start\ndata: {\"type\":\"message_start\"}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		io.WriteString(w, wire)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(closed)
	}))
	defer server.Close()
	owner := cooperative.New()
	defer owner.Close()
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		reads := 0
		driver, _ := NewOwnerOpenIO(func(kind OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
			if kind == OwnerBodyRead {
				reads++
			}
			return readAwait(scope, kind, work)
		})
		stream, err := NewAnthropic(AnthropicConfig{BaseURL: server.URL, APIKey: "private-key", Timeout: time.Second, HTTPExchange: &RawHTTP1Exchange{}, OwnerOpenIO: driver}).OpenMessagesStream(context.Background(), &MessagesRequest{Body: []byte(`{}`), Path: "/v1/messages?beta=true"})
		if err != nil {
			return err
		}
		defer stream.Close()
		for _, wanted := range []string{`{"type":"message_start"}`, `{"type":"message_stop"}`} {
			raw, err := stream.Next()
			if err != nil || string(raw) != wanted {
				return fmt.Errorf("buffered event %s %v", raw, err)
			}
			if reads != 1 {
				return fmt.Errorf("read ahead/extra await %d", reads)
			}
		}
		time.Sleep(10 * time.Millisecond)
		if reads != 1 {
			return fmt.Errorf("background read %d", reads)
		}
		stream.Close()
		if driver.LeaseCount() != 0 {
			return errors.New("lease leaked")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("actual held peer not closed")
	}
}
func TestOwnerNativeReadBytesBeforeUnexpectedEOF(t *testing.T) {
	wire := "event: message_start\ndata: {\"type\":\"message_start\"}\n\nevent: message_stop\ndata: {}\r\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		fmt.Fprintf(buffer, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nContent-Length: 9999\r\nConnection: close\r\n\r\n%s", wire)
		buffer.Flush()
		conn.Close()
	}))
	defer server.Close()
	owner := cooperative.New()
	defer owner.Close()
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		driver, _ := NewOwnerOpenIO(func(kind OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
			return readAwait(scope, kind, work)
		})
		stream, err := NewAnthropic(AnthropicConfig{BaseURL: server.URL, APIKey: "private-key", Timeout: time.Second, HTTPExchange: &RawHTTP1Exchange{}, OwnerOpenIO: driver}).OpenMessagesStream(context.Background(), &MessagesRequest{Body: []byte(`{}`), Path: "/v1/messages?beta=true"})
		if err != nil {
			return err
		}
		defer stream.Close()
		raw, err := stream.Next()
		if err != nil || string(raw) != `{"type":"message_start"}` {
			return fmt.Errorf("complete bytes dropped %s %v", raw, err)
		}
		raw, err = stream.Next()
		if !errors.Is(err, io.ErrUnexpectedEOF) || len(raw) != 0 || stream.AbortCause() != nil {
			return fmt.Errorf("nonEOF incorrectly flushed tail %s %v", raw, err)
		}
		if driver.LeaseCount() != 0 {
			return errors.New("failed read retained socket")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestOwnerNativeEOFRemainsExactAndDoesNotInventDispatch(t *testing.T) {
	wire := "event: message_start\ndata: {\"type\":\"message_start\"}\n\nevent: message_stop\ndata: {}\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(wire)))
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, wire)
	}))
	defer server.Close()
	owner := cooperative.New()
	defer owner.Close()
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		driver, _ := NewOwnerOpenIO(func(kind OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
			return readAwait(scope, kind, work)
		})
		stream, err := NewAnthropic(AnthropicConfig{BaseURL: server.URL, APIKey: "private-key", Timeout: time.Second, HTTPExchange: &RawHTTP1Exchange{}, OwnerOpenIO: driver}).OpenMessagesStream(context.Background(), &MessagesRequest{Body: []byte(`{}`), Path: "/v1/messages?beta=true"})
		if err != nil {
			return err
		}
		defer stream.Close()
		raw, err := stream.Next()
		if err != nil || string(raw) != `{"type":"message_start"}` {
			return fmt.Errorf("completed event %s %v", raw, err)
		}
		raw, err = stream.Next()
		if err != io.EOF || len(raw) != 0 || stream.AbortCause() != nil {
			return fmt.Errorf("EOF identity/dispatch %s %v", raw, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestOwnerCloseJoinsBlockedNativeRead(t *testing.T) {
	peerClosed := make(chan struct{})
	entered := make(chan struct{})
	done := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(peerClosed)
	}))
	defer server.Close()
	owner := cooperative.New()
	defer owner.Close()
	go func() {
		done <- owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			driver, _ := NewOwnerOpenIO(func(kind OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
				return readAwait(scope, kind, work)
			})
			stream, err := NewAnthropic(AnthropicConfig{BaseURL: server.URL, APIKey: "private-key", Timeout: time.Second, HTTPExchange: &RawHTTP1Exchange{}, OwnerOpenIO: driver}).OpenMessagesStream(context.Background(), &MessagesRequest{Body: []byte(`{}`), Path: "/v1/messages?beta=true"})
			if err != nil {
				return err
			}
			defer stream.Close()
			lease := soleReadLease(driver)
			lease.body = &readStartObserver{ReadCloser: lease.body, entered: entered}
			_, err = stream.Next()
			if !IsOwnerWaitFailure(err) || stream.AbortCause() != nil {
				return fmt.Errorf("admin close projected as model cancellation %v", err)
			}
			if driver.LeaseCount() != 0 {
				return errors.New("admin Close retained lease")
			}
			return err
		})
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("native read not entered")
	}
	owner.Close()
	if err := <-done; !errors.Is(err, cooperative.ErrClosed) {
		t.Fatalf("owner shutdown %v", err)
	}
	select {
	case <-peerClosed:
	case <-time.After(time.Second):
		t.Fatal("joined read retained actual peer")
	}
}
func TestOwnerReadDiscardIsAdministrativeEvenWithOriginalCallerAborted(t *testing.T) {
	rejected := errors.New("owner read completion discarded")
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: message_stop\ndata: {}\n\n")
	}))
	defer server.Close()
	owner := cooperative.New()
	defer owner.Close()
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		driver, _ := NewOwnerOpenIO(func(kind OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
			raw, err := readAwait(scope, kind, work)
			if kind == OwnerBodyRead && len(raw) > 0 {
				cancel()
				return raw, rejected
			}
			return raw, err
		})
		stream, err := NewAnthropic(AnthropicConfig{BaseURL: server.URL, APIKey: "private-key", Timeout: time.Second, HTTPExchange: &RawHTTP1Exchange{}, OwnerOpenIO: driver}).OpenMessagesStream(caller, &MessagesRequest{Body: []byte(`{}`), Path: "/v1/messages?beta=true"})
		if err != nil {
			return err
		}
		defer stream.Close()
		raw, err := stream.Next()
		if !IsOwnerWaitFailure(err) || !errors.Is(err, rejected) || len(raw) > 0 || stream.AbortCause() != nil {
			return fmt.Errorf("discard became network/abort %s %v", raw, err)
		}
		if driver.LeaseCount() != 0 {
			return errors.New("discard retained lease")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestOwnerStreamBodyRejectsForeignResourceAndOversizedBridge(t *testing.T) {
	driver, _ := NewOwnerOpenIO(func(OwnerIOWait, func(context.Context) ([]byte, error)) ([]byte, error) {
		return []byte("too many bytes"), nil
	})
	foreign, _ := NewOwnerOpenIO(func(OwnerIOWait, func(context.Context) ([]byte, error)) ([]byte, error) {
		t.Fatal("foreign await invoked")
		return nil, nil
	})
	for _, body := range []io.ReadCloser{io.NopCloser(strings.NewReader("foreign")), &ownerLease{driver: foreign}} {
		if _, err := driver.streamBody(body); !errors.Is(err, ErrHTTPExchangeProfile) {
			t.Fatal("unknown/foreign lease admitted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	lease := driver.reserve(ctx, nil)
	lease.body = io.NopCloser(strings.NewReader("original"))
	body, err := driver.streamBody(lease)
	if err != nil {
		t.Fatal(err)
	}
	out := []byte("xx")
	n, err := body.Read(out)
	if n != 0 || !errors.Is(err, ErrHTTPExchangeProfile) || string(out) != "xx" || driver.LeaseCount() != 0 {
		t.Fatalf("oversized immutable result %d %s %v", n, out, err)
	}
	cancel()
}
func TestOwnerReadAfterLeaseClosedIsPhysicalClosure(t *testing.T) {
	driver, _ := NewOwnerOpenIO(func(_ OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
		return work(context.Background())
	})
	lease := driver.reserve(context.Background(), nil)
	body, err := driver.streamBody(lease)
	if err != nil {
		t.Fatal(err)
	}
	body.Close()
	n, err := body.Read(make([]byte, 4))
	if n != 0 || !errors.Is(err, net.ErrClosed) || IsOwnerWaitFailure(err) {
		t.Fatalf("closed native read %d %v", n, err)
	}
}
