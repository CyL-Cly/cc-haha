//go:build darwin

package provider

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"syscall"
	"testing"
)

func TestNativeTLSReservationRejectsInvalidAdmissionWithoutResource(t *testing.T) {
	_, trust := tlsProbeConfiguration(t)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name      string
		ctx       context.Context
		authority string
		wire      []byte
		limit     int
		trust     *tls.Config
		want      error
	}{
		{"missing original context", nil, "127.0.0.1:1", []byte("original"), 64, trust, ErrHTTPExchangeProfile},
		{"unbounded original request", context.Background(), "127.0.0.1:1", bytes.Repeat([]byte("x"), 8*1024*1024+1), 64, trust, ErrHTTPExchangeProfile},
		{"missing TCP authority", context.Background(), "127.0.0.1", []byte("original"), 64, trust, ErrHTTPExchangeProfile},
		{"cancelled before native admission", cancelled, "127.0.0.1:1", []byte("original"), 64, trust, context.Canceled},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			resource, err := newNativeTLSHTTP1Reservation(item.ctx, item.authority, item.wire, item.limit, item.trust)
			if resource != nil || !errors.Is(err, item.want) {
				t.Fatal("unadmitted TLS resource", resource, err)
			}
		})
	}
}
func TestNativeTLSReservationDialFaultIsJoined(t *testing.T) {
	_, trust := tlsProbeConfiguration(t)
	listener := tlsProbeListener(t)
	address := listener.Addr().String()
	listener.Close()
	resource, err := newNativeTLSHTTP1Reservation(context.Background(), address, []byte("original"), 64, trust)
	if err != nil {
		t.Fatal(err)
	}
	if err = resource.WaitReadable(context.Background()); err == nil {
		t.Fatal("closed private listener accepted TLS dial")
	}
	if err = resource.Close(); err != nil {
		t.Fatal(err)
	}
	state, err := InspectNativeOwnerBody(&nativeOwnerBody{resource: resource})
	if err != nil {
		t.Fatal(err)
	}
	if !state.Closed || !state.Joined || state.PendingReadiness {
		t.Fatal(state)
	}
}
func TestNativeTLSRecordWriteBackpressureNeverBlocksOwnerOrRetries(t *testing.T) {
	listener := tlsProbeListener(t)
	physical, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer physical.Close()
	peer, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	socket, err := newNativeHTTP1Socket(physical.(*net.TCPConn))
	if err != nil {
		t.Fatal(err)
	}
	defer socket.close()
	if err = syscall.SetsockoptInt(socket.descriptor, syscall.SOL_SOCKET, syscall.SO_SNDBUF, 1024); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("private encrypted record bytes"), 1024*1024)
	count, err := nativeTLSWrite(socket, payload)
	if count >= len(payload) || !errors.Is(err, ErrHTTPExchangeProfile) {
		t.Fatal("blocking/full write hid native backpressure", count, err)
	}
	// The helper reports physical partial progress unchanged. No fabricated full
	// write, extra request, retry or sleep is used to conceal the capability.
	socket.shutdown()
	socket.close()
	count, err = nativeTLSWrite(socket, []byte("original"))
	if count != 0 || !errors.Is(err, syscall.EBADF) {
		t.Fatal("closed actual descriptor lost native error", count, err)
	}
}
func TestNativeTLSRecordStreamRetainsActualWriteFault(t *testing.T) {
	serverConfig, clientConfig := tlsProbeConfiguration(t)
	listener := tlsProbeListener(t)
	serverDone := make(chan error, 1)
	go func() {
		peer, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer peer.Close()
		secured := tls.Server(peer, serverConfig)
		if err = secured.Handshake(); err != nil {
			serverDone <- err
			return
		}
		_, err = io.Copy(io.Discard, secured)
		serverDone <- err
	}()
	physical, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer physical.Close()
	socket, err := newNativeHTTP1Socket(physical.(*net.TCPConn))
	if err != nil {
		t.Fatal(err)
	}
	defer socket.close()
	wrapped := &nativeTLSConn{Conn: physical, socket: socket}
	secured := tls.Client(wrapped, clientConfig)
	if err = secured.Handshake(); err != nil {
		t.Fatal(err)
	}
	wrapped.native.Store(true)
	// Real resource cancellation closes the descriptor while crypto/tls still has
	// its completed handshake. The attempted TLS write must retain that fault.
	socket.shutdown()
	socket.close()
	_, err = secured.Write([]byte("actual original TLS payload"))
	if !errors.Is(err, syscall.EBADF) {
		t.Fatal("cancelled TLS write fault", err)
	}
	stream := &nativeTLSHTTP1Stream{secured: secured, wrapped: wrapped}
	_, readErr := stream.read(make([]byte, 8))
	if readErr != wrapped.writeFault.Load().err {
		t.Fatal("TLS read swallowed preceding native write failure", readErr, err)
	}
	physical.Close()
	<-serverDone
}

func TestNativeTLSActualEmptyCheckpointResumesSameRecordsAndBody(t *testing.T) {
	serverConfig, trust := tlsProbeConfiguration(t)
	listener := tlsProbeListener(t)
	requestRead := make(chan struct{})
	release := make(chan struct{})
	peerDone := make(chan error, 1)
	go func() {
		peer, err := listener.Accept()
		if err != nil {
			peerDone <- err
			return
		}
		defer peer.Close()
		secured := tls.Server(peer, serverConfig)
		request, err := http.ReadRequest(bufio.NewReader(secured))
		if err != nil {
			peerDone <- err
			return
		}
		request.Body.Close()
		close(requestRead)
		<-release
		_, err = io.WriteString(secured, "HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\nsame")
		if err == nil {
			_, err = io.Copy(io.Discard, peer)
		}
		peerDone <- err
	}()
	resource, err := newNativeTLSHTTP1Reservation(context.Background(), listener.Addr().String(), []byte("GET /original HTTP/1.1\r\nHost: private\r\n\r\n"), 128, trust)
	if err != nil {
		t.Fatal(err)
	}
	defer resource.Close()
	<-requestRead
	if header, ready, err := resource.Checkpoint(); err != nil || ready || len(header) > 0 {
		t.Fatal("absent encrypted response invented readiness", header, ready, err)
	}
	close(release)
	for {
		if err = resource.WaitReadable(context.Background()); err != nil {
			t.Fatal(err)
		}
		header, ready, failure := resource.Checkpoint()
		if failure != nil {
			t.Fatal(failure)
		}
		if ready {
			if string(header) != "HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\n" {
				t.Fatal(string(header))
			}
			break
		}
	}
	request, _ := http.NewRequest("GET", "https://"+listener.Addr().String()+"/original", nil)
	response, err := resource.AdoptResponse(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "same" {
		t.Fatal("same TLS lease lost read-past-header bytes", string(body), err)
	}
	response.Body.Close()
	resource.Close()
	if err = <-peerDone; err != nil {
		t.Fatal(err)
	}
}

func TestNativeTLSActualLargeRequestWriteFailureIsJoined(t *testing.T) {
	serverConfig, trust := tlsProbeConfiguration(t)
	listener := tlsProbeListener(t)
	peerDone := make(chan error, 1)
	go func() {
		peer, err := listener.Accept()
		if err != nil {
			peerDone <- err
			return
		}
		secured := tls.Server(peer, serverConfig)
		one := make([]byte, 1)
		_, err = io.ReadFull(secured, one)
		// The peer has seen the actual first encrypted request record. This is a
		// physical reset, not a fabricated readiness callback or completion-as-ready.
		peer.(*net.TCPConn).SetLinger(0)
		peer.Close()
		peerDone <- err
	}()
	resource, err := newNativeTLSHTTP1Reservation(context.Background(), listener.Addr().String(), bytes.Repeat([]byte("original"), 1024*1024-1), 128, trust)
	if err != nil {
		t.Fatal(err)
	}
	<-resource.done // Here completion is only the pure writer's error/join fact.
	resource.mu.Lock()
	failure := resource.failure
	resource.mu.Unlock()
	if failure == nil {
		resource.Close()
		t.Fatal("closed peer silently accepted entire original request")
	}
	t.Logf("actual transport write error: %T %v", failure, failure)
	if err = resource.Close(); err != nil {
		t.Fatal(err)
	}
	if err = <-peerDone; err != nil {
		t.Fatal(err)
	}
}
