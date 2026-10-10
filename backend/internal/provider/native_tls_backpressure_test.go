//go:build darwin

package provider

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestNativeTLSActualSlowPeerLargeRequestRetainsExactBodyAndResponse(t *testing.T) {
	serverConfig, trust := tlsProbeConfiguration(t)
	listener := tlsProbeListener(t)
	handshake := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	payload := bytes.Repeat([]byte("actual model tools and user context 😀\n"), 120000)
	go func() {
		peer, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer peer.Close()
		_ = peer.SetDeadline(time.Now().Add(12 * time.Second))
		if err = peer.(*net.TCPConn).SetReadBuffer(4096); err != nil {
			done <- err
			return
		}
		secured := tls.Server(peer, serverConfig)
		if err = secured.Handshake(); err != nil {
			done <- err
			return
		}
		close(handshake)
		<-release
		request, err := http.ReadRequest(bufio.NewReader(secured))
		if err != nil {
			done <- err
			return
		}
		actual, err := io.ReadAll(request.Body)
		if err != nil {
			done <- err
			return
		}
		if !bytes.Equal(actual, payload) {
			done <- fmt.Errorf("physical request bytes/hash mismatch len=%d want=%d hash=%x want=%x", len(actual), len(payload), sha256.Sum256(actual), sha256.Sum256(payload))
			return
		}
		_, err = io.WriteString(secured, "HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\nsame")
		if err == nil {
			_, err = io.Copy(io.Discard, peer)
		}
		done <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	wire := append([]byte(fmt.Sprintf("POST /messages HTTP/1.1\r\nHost: private\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", len(payload))), payload...)
	resource, err := newNativeTLSHTTP1Reservation(ctx, listener.Addr().String(), wire, 4096, trust)
	if err != nil {
		t.Fatal(err)
	}
	defer resource.Close()
	select {
	case <-handshake:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// The published owner checkpoint must remain nonblocking while the peer
	// deliberately withholds request reads. Initial write completion is not
	// the definition of response readiness.
	started := time.Now()
	if header, ready, err := resource.Checkpoint(); err != nil || ready || len(header) != 0 {
		close(release)
		t.Fatal("owner checkpoint before response", header, ready, err)
	}
	if time.Since(started) > 200*time.Millisecond {
		close(release)
		t.Fatal("owner checkpoint waited for writable transport")
	}
	time.Sleep(40 * time.Millisecond)
	close(release)
	for {
		if err = resource.WaitReadable(ctx); err != nil {
			t.Fatal(err)
		}
		_, ready, err := resource.Checkpoint()
		if err != nil {
			t.Fatal(err)
		}
		if ready {
			break
		}
	}
	request, _ := http.NewRequest("POST", "https://"+listener.Addr().String()+"/messages", nil)
	response, err := resource.AdoptResponse(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "same" {
		t.Fatal("same physical response/body", string(body), err)
	}
	if err = resource.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("peer/join", ctx.Err())
	}
}
