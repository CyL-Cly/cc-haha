//go:build darwin

package provider

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type reservationPeer struct {
	listener net.Listener
	parsed   chan struct{}
	reply    chan string
	written  chan struct{}
	joined   chan error
}

func newReservationPeer(t *testing.T) *reservationPeer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	peer := &reservationPeer{listener: listener, parsed: make(chan struct{}), reply: make(chan string, 1), written: make(chan struct{}), joined: make(chan error, 1)}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			peer.joined <- err
			return
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
		request, err := http.ReadRequest(bufio.NewReader(connection))
		if err != nil {
			peer.joined <- err
			return
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			peer.joined <- err
			return
		}
		if request.Method != "POST" || request.URL.Path != "/anthropic/v1/messages" || string(body) != "{}" {
			peer.joined <- errors.New("original raw request changed")
			return
		}
		close(peer.parsed)
		wire := <-peer.reply
		if wire != "" {
			if _, err = io.WriteString(connection, wire); err != nil {
				peer.joined <- err
				return
			}
		}
		close(peer.written)
		remaining, err := io.ReadAll(connection)
		if err == nil && len(remaining) != 0 {
			err = errors.New("request was written more than once")
		}
		peer.joined <- err
	}()
	return peer
}

func reservationWire(authority string) []byte {
	return []byte("POST /anthropic/v1/messages HTTP/1.1\r\nHost: " + authority + "\r\nContent-Length: 2\r\n\r\n{}")
}

func (peer *reservationPeer) join(t *testing.T) {
	t.Helper()
	select {
	case err := <-peer.joined:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("actual peer not joined after resource close")
	}
}

func TestReservationPublishesResponseResourceBeforePhysicalRequest(t *testing.T) {
	peer := newReservationPeer(t)
	authority := peer.listener.Addr().String()
	reservation, err := NewNativeHTTP1Reservation(context.Background(), authority, reservationWire(authority), 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Close()
	<-peer.parsed
	reservation.mu.Lock()
	operation := reservation.operation
	reservation.mu.Unlock()
	if operation == nil {
		t.Fatal("server consumed request before owner could know response descriptor")
	}
	peer.reply <- "HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\nsame"
	<-peer.written
	if err := reservation.WaitReadable(context.Background()); err != nil {
		t.Fatal(err)
	}
	header, ready, err := reservation.Checkpoint()
	if err != nil || !ready || string(header) != "HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\n" {
		t.Fatalf("fresh headers: %q %v %v", header, ready, err)
	}
	request, _ := http.NewRequest(http.MethodPost, "http://"+authority+"/anthropic/v1/messages", nil)
	response, err := reservation.AdoptResponse(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "same" {
		t.Fatalf("same body: %q %v", body, err)
	}
	if err = response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	peer.join(t)
	if _, _, err = reservation.Checkpoint(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed checkpoint: %v", err)
	}
	if err = reservation.WaitReadable(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed wait: %v", err)
	}
	if _, err = reservation.AdoptResponse(request); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed adoption: %v", err)
	}
}

func TestReservationCancelledTemporaryWaitRetainsActualBody(t *testing.T) {
	peer := newReservationPeer(t)
	authority := peer.listener.Addr().String()
	reservation, err := NewNativeHTTP1Reservation(context.Background(), authority, reservationWire(authority), 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Close()
	<-peer.parsed
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- reservation.WaitReadable(ctx) }()
	reservation.mu.Lock()
	operation := reservation.operation
	reservation.mu.Unlock()
	nativeWaitRegistered(t, operation)
	cancel()
	if err = <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("temporary wait: %v", err)
	}
	peer.reply <- "HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\nlive"
	<-peer.written
	if err := reservation.WaitReadable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ready, err := reservation.Checkpoint(); !ready || err != nil {
		t.Fatalf("body lifetime: %v %v", ready, err)
	}
	response, err := reservation.AdoptResponse(nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "live" {
		t.Fatalf("body lost: %q %v", body, err)
	}
	_ = response.Body.Close()
	peer.join(t)
}

func TestReservationCloseJoinsActualNativeWaitAndRequestWorker(t *testing.T) {
	peer := newReservationPeer(t)
	authority := peer.listener.Addr().String()
	reservation, err := NewNativeHTTP1Reservation(context.Background(), authority, reservationWire(authority), 1024)
	if err != nil {
		t.Fatal(err)
	}
	<-peer.parsed
	finished := make(chan error, 1)
	go func() { finished <- reservation.WaitReadable(context.Background()) }()
	reservation.mu.Lock()
	operation := reservation.operation
	reservation.mu.Unlock()
	nativeWaitRegistered(t, operation)
	peer.reply <- ""
	<-peer.written
	if err = reservation.Close(); err != nil {
		t.Fatal(err)
	}
	if err = <-finished; !errors.Is(err, net.ErrClosed) {
		t.Fatalf("original pending wait: %v", err)
	}
	select {
	case <-reservation.done:
	default:
		t.Fatal("pure dial/write not joined")
	}
	peer.join(t)
}

func TestReservationNativePartialThenSameResponse(t *testing.T) {
	operation, peer := nativeOperation(t, 1024)
	reservation := &NativeHTTP1Reservation{operation: operation, available: make(chan struct{}), done: make(chan struct{}), cancel: func() {}}
	reservation.announce()
	close(reservation.done)
	defer reservation.Close()
	nativeWrite(t, peer, "HTTP/1.1 200 OK\r\nContent-Length: 4\r\n")
	nativeReady(t, operation)
	if header, ready, err := reservation.Checkpoint(); ready || err != nil || header != nil {
		t.Fatalf("partial: %q %v %v", header, ready, err)
	}
	nativeWrite(t, peer, "\r\nsame")
	nativeReady(t, operation)
	if _, ready, err := reservation.Checkpoint(); !ready || err != nil {
		t.Fatalf("full: %v %v", ready, err)
	}
	response, err := reservation.AdoptResponse(nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "same" {
		t.Fatalf("retained bytes: %q %v", body, err)
	}
}

func TestReservationOriginalDialFaultIsRetained(t *testing.T) {
	// Port zero cannot name a listening service; no released-port race is used.
	authority := "127.0.0.1:0"
	reservation, err := NewNativeHTTP1Reservation(context.Background(), authority, reservationWire(authority), 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Close()
	<-reservation.done
	_, ready, actual := reservation.Checkpoint()
	var physical *net.OpError
	if ready || !errors.As(actual, &physical) || physical.Op != "dial" {
		t.Fatalf("original dial fault: %v %v", ready, actual)
	}
	if again := reservation.WaitReadable(context.Background()); again != actual {
		t.Fatalf("fault identity changed: %v / %v", actual, again)
	}
	if _, err = reservation.AdoptResponse(nil); !errors.Is(err, ErrHTTPExchangeProfile) {
		t.Fatalf("unpublished adoption: %v", err)
	}
}

func TestReservationHeadersDoNotRequireWriteWorkerCompletion(t *testing.T) {
	connection, peer := nativePair(t)
	wire := reservationWire("127.0.0.1:1")
	if _, err := connection.Write(wire); err != nil {
		t.Fatal(err)
	}
	request, err := http.ReadRequest(bufio.NewReader(peer))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(request.Body)
	if err != nil || string(body) != "{}" {
		t.Fatalf("actual POST bytes: %q %v", body, err)
	}
	operation, err := NewNativeHTTP1Operation(connection, 1024)
	if err != nil {
		t.Fatal(err)
	}
	reservation := &NativeHTTP1Reservation{operation: operation, available: make(chan struct{}), done: make(chan struct{}), cancel: func() {}}
	reservation.announce()
	defer func() { close(reservation.done); _ = reservation.Close() }()
	nativeWrite(t, peer, "HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\nsame")
	nativeReady(t, operation)
	select {
	case <-reservation.done:
		t.Fatal("fixture did not retain unacknowledged completion")
	default:
	}
	header, ready, err := reservation.Checkpoint()
	if err != nil || !ready || len(header) == 0 {
		t.Fatalf("worker publication required despite fresh native headers: %q %v %v", header, ready, err)
	}
	response, err := reservation.AdoptResponse(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(response.Body)
	if err != nil || string(body) != "same" {
		t.Fatalf("same reserved response: %q %v", body, err)
	}
}

func TestReservationFaultIdentityAndUnprovedReadyTie(t *testing.T) {
	for _, full := range []bool{false, true} {
		t.Run(fmt.Sprintf("complete-%v", full), func(t *testing.T) {
			operation, peer := nativeOperation(t, 1024)
			reservation := &NativeHTTP1Reservation{operation: operation, available: make(chan struct{}), done: make(chan struct{}), cancel: func() {}}
			reservation.announce()
			close(reservation.done)
			defer reservation.Close()
			failure := errors.New("retained original request write fault")
			reservation.publishFailure(failure)
			if full {
				nativeWrite(t, peer, "HTTP/1.1 200 OK\r\n\r\n")
				nativeReady(t, operation)
			}
			_, ready, err := reservation.Checkpoint()
			if ready {
				t.Fatal("unproved write fault/header tie was admitted")
			}
			if full {
				if !errors.Is(err, ErrHTTPExchangeProfile) {
					t.Fatalf("missing explicit tie capability: %v", err)
				}
			} else if err != failure {
				t.Fatalf("original fault identity changed: %v", err)
			}
		})
	}
}

func TestReservationActualWriteFailureIsRetained(t *testing.T) {
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	joined := make(chan error, 1)
	go func() {
		peer, err := listener.AcceptTCP()
		if err != nil {
			joined <- err
			return
		}
		_ = peer.SetLinger(0)
		scratch := make([]byte, 1)
		_, err = io.ReadFull(peer, scratch)
		_ = peer.Close()
		joined <- err
	}()
	reservation, err := NewNativeHTTP1Reservation(context.Background(), listener.Addr().String(), make([]byte, 8*1024*1024), 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Close()
	<-reservation.done
	if err = <-joined; err != nil {
		t.Fatal(err)
	}
	reservation.mu.Lock()
	original := reservation.failure
	reservation.mu.Unlock()
	var physical *net.OpError
	if !errors.As(original, &physical) || physical.Op != "write" {
		t.Fatalf("actual write failure missing: %v", original)
	}
	if _, ready, err := reservation.Checkpoint(); ready || err != original {
		t.Fatalf("write fault identity: %v %v / %v", ready, err, original)
	}
}

type reservationObservedContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (ctx *reservationObservedContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.observed) })
	return ctx.Context.Done()
}

func TestReservationPendingResourceWaitCancellationAndPublication(t *testing.T) {
	t.Run("cancel-pending-resource", func(t *testing.T) {
		base, cancel := context.WithCancel(context.Background())
		ctx := &reservationObservedContext{Context: base, observed: make(chan struct{})}
		reservation := &NativeHTTP1Reservation{available: make(chan struct{}), done: make(chan struct{}), cancel: func() {}}
		finished := make(chan error, 1)
		go func() { finished <- reservation.WaitReadable(ctx) }()
		<-ctx.observed
		cancel()
		if err := <-finished; !errors.Is(err, context.Canceled) {
			t.Fatalf("pending publication cancel: %v", err)
		}
		close(reservation.done)
		_ = reservation.Close()
	})
	t.Run("real-resource-becomes-known", func(t *testing.T) {
		ctx := &reservationObservedContext{Context: context.Background(), observed: make(chan struct{})}
		reservation := &NativeHTTP1Reservation{available: make(chan struct{}), done: make(chan struct{}), cancel: func() {}}
		finished := make(chan error, 1)
		go func() { finished <- reservation.WaitReadable(ctx) }()
		<-ctx.observed
		operation, peer := nativeOperation(t, 1024)
		reservation.mu.Lock()
		reservation.operation = operation
		reservation.mu.Unlock()
		reservation.announce()
		nativeWrite(t, peer, "HTTP/1.1 200 OK\r\n\r\n")
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
		if _, ready, err := reservation.Checkpoint(); !ready || err != nil {
			t.Fatalf("actual publication: %v %v", ready, err)
		}
		close(reservation.done)
		_ = reservation.Close()
	})
}

func TestReservationAdoptionKeepsOriginalParserFailure(t *testing.T) {
	operation, peer := nativeOperation(t, 1024)
	reservation := &NativeHTTP1Reservation{operation: operation, available: make(chan struct{}), done: make(chan struct{}), cancel: func() {}}
	close(reservation.done)
	defer reservation.Close()
	nativeWrite(t, peer, "NOT HTTP\r\n\r\n")
	nativeReady(t, operation)
	if _, ready, err := reservation.Checkpoint(); !ready || err != nil {
		t.Fatalf("physical header terminator: %v %v", ready, err)
	}
	if _, err := reservation.AdoptResponse(nil); err == nil || errors.Is(err, ErrHTTPExchangeProfile) {
		t.Fatalf("native parser failure changed: %v", err)
	}
}

func TestReservationClosedDuringDialSendsNoLateRequest(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	joined := make(chan error, 1)
	go func() {
		peer, err := listener.Accept()
		if err != nil {
			joined <- err
			return
		}
		defer peer.Close()
		_ = peer.SetDeadline(time.Now().Add(3 * time.Second))
		body, err := io.ReadAll(peer)
		if err == nil && len(body) != 0 {
			err = errors.New("late request escaped closed reservation")
		}
		joined <- err
	}()
	// A physical dial can finish just as the owner closes its reservation. This
	// seeds that closed owner state, then exercises the real late TCP result.
	reservation := &NativeHTTP1Reservation{closed: true, available: make(chan struct{}), done: make(chan struct{}), cancel: func() {}}
	authority := listener.Addr().String()
	go reservation.dialAndWrite(context.Background(), authority, reservationWire(authority), 1024)
	<-reservation.done
	if err = <-joined; err != nil {
		t.Fatal(err)
	}
	reservation.mu.Lock()
	operation := reservation.operation
	reservation.mu.Unlock()
	if operation != nil {
		t.Fatal("closed owner adopted late response resource")
	}
}

func TestReservationAdmitsNoImplicitTransportFallback(t *testing.T) {
	for _, test := range []struct {
		ctx     context.Context
		address string
		wire    []byte
		limit   int
	}{
		{nil, "127.0.0.1:1", []byte("x"), 1024}, {context.Background(), "missing-port", []byte("x"), 1024},
		{context.Background(), "127.0.0.1:1", nil, 1024}, {context.Background(), "127.0.0.1:1", []byte("x"), 3},
		{context.Background(), "127.0.0.1:1", []byte("x"), 1024*1024 + 1},
		{context.Background(), "127.0.0.1:1", []byte(strings.Repeat("x", 8*1024*1024+1)), 1024},
	} {
		if _, err := NewNativeHTTP1Reservation(test.ctx, test.address, test.wire, test.limit); !errors.Is(err, ErrHTTPExchangeProfile) {
			t.Fatalf("unsupported input: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewNativeHTTP1Reservation(ctx, "127.0.0.1:1", []byte("x"), 1024); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled admission: %v", err)
	}
	reservation := &NativeHTTP1Reservation{available: make(chan struct{}), done: make(chan struct{}), cancel: func() {}}
	if _, ready, err := reservation.Checkpoint(); ready || err != nil {
		t.Fatalf("pending publication: %v %v", ready, err)
	}
	if err := reservation.WaitReadable(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled demand: %v", err)
	}
	close(reservation.done)
	if err := reservation.Close(); err != nil {
		t.Fatal(err)
	}
}
