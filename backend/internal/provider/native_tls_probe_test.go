package provider

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"os"
	"sync/atomic"
	"testing"
)

type tlsProbeWouldBlock struct{}

func (*tlsProbeWouldBlock) Error() string   { return "actual native TLS socket has no readable bytes" }
func (*tlsProbeWouldBlock) Temporary() bool { return true }
func (*tlsProbeWouldBlock) Timeout() bool   { return false }

type tlsProbeConn struct {
	net.Conn
	socket     *nativeHTTP1Socket
	native     atomic.Bool
	checkpoint atomic.Bool
}

func (connection *tlsProbeConn) Read(out []byte) (int, error) {
	if !connection.native.Load() {
		return connection.Conn.Read(out)
	}
	count, err := connection.socket.read(out)
	if count == 0 && err == nil {
		return 0, &tlsProbeWouldBlock{}
	}
	return count, err
}
func (connection *tlsProbeConn) Write(out []byte) (int, error) {
	if connection.checkpoint.Load() {
		return 0, errors.New("probe rejects owner TLS transport write")
	}
	return connection.Conn.Write(out)
}
func tlsProbeConfiguration(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	cert, err := tls.LoadX509KeyPair("../../../scripts/quality-gate/go-parity/testdata/private-loopback-certificate.pem", "../../../scripts/quality-gate/go-parity/testdata/private-loopback-key.pem")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../../scripts/quality-gate/go-parity/testdata/private-loopback-certificate.pem")
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(raw) {
		t.Fatal("fixture root missing")
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}}, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12, ServerName: "127.0.0.1", NextProtos: []string{"http/1.1"}}
}
func tlsProbeListener(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	return listener
}
func tlsProbeNativeClient(t *testing.T, listener net.Listener, config *tls.Config) (*tls.Conn, *tlsProbeConn) {
	t.Helper()
	physical, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	socket, err := newNativeHTTP1Socket(physical.(*net.TCPConn))
	if err != nil {
		t.Fatal(err)
	}
	wrapped := &tlsProbeConn{Conn: physical, socket: socket}
	t.Cleanup(func() { physical.Close(); socket.close() })
	return tls.Client(wrapped, config), wrapped
}
func TestTLSPrimitiveHandshakeTemporaryReadIsSticky(t *testing.T) {
	serverConfig, clientConfig := tlsProbeConfiguration(t)
	listener := tlsProbeListener(t)
	accepted := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		close(accepted)
		<-release
		secured := tls.Server(conn, serverConfig)
		done <- secured.Handshake()
	}()
	client, raw := tlsProbeNativeClient(t, listener, clientConfig)
	<-accepted
	raw.native.Store(true)
	first := client.Handshake()
	var temporary net.Error
	if !errors.As(first, &temporary) || !temporary.Temporary() {
		t.Fatal("real native no-readable handshake did not return temporary", first)
	}
	close(release)
	second := client.Handshake()
	if second != first {
		t.Fatal("Go handshake unexpectedly resumed instead of retaining original failure", first, second)
	}
	raw.socket.shutdown()
	raw.Conn.Close()
	<-done
}

type tlsProbeFragmentWriter struct {
	net.Conn
	active     atomic.Bool
	cut        int
	prefixSent chan struct{}
	remainder  chan struct{}
	corrupt    bool
}

func (connection *tlsProbeFragmentWriter) Write(out []byte) (int, error) {
	if !connection.active.Swap(false) {
		return connection.Conn.Write(out)
	}
	copyWire := append([]byte(nil), out...)
	if connection.corrupt {
		copyWire[len(copyWire)-1] ^= 1
	}
	count, err := connection.Conn.Write(copyWire[:connection.cut])
	close(connection.prefixSent)
	<-connection.remainder
	if err != nil {
		return count, err
	}
	rest, err := connection.Conn.Write(copyWire[connection.cut:])
	return count + rest, err
}
func TestTLSPrimitiveCompletedHandshakeReadResumesNativePartialRecord(t *testing.T) {
	for _, cut := range []int{2, 8} {
		t.Run(string(rune('0'+cut)), func(t *testing.T) {
			serverConfig, clientConfig := tlsProbeConfiguration(t)
			listener := tlsProbeListener(t)
			prefix, remainder := make(chan struct{}), make(chan struct{})
			serverDone := make(chan error, 1)
			handshake := make(chan struct{})
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					serverDone <- err
					return
				}
				defer conn.Close()
				wrapped := &tlsProbeFragmentWriter{Conn: conn, cut: cut, prefixSent: prefix, remainder: remainder}
				secured := tls.Server(wrapped, serverConfig)
				if err = secured.Handshake(); err != nil {
					serverDone <- err
					return
				}
				close(handshake)
				wrapped.active.Store(true)
				_, err = secured.Write([]byte("HTTP/1.1 200 OK\r\ncontent-length: 4\r\n\r\nbody"))
				serverDone <- err
			}()
			client, raw := tlsProbeNativeClient(t, listener, clientConfig)
			if err := client.Handshake(); err != nil {
				t.Fatal(err)
			}
			<-handshake
			raw.native.Store(true)
			<-prefix
			if err := raw.socket.wait(); err != nil {
				t.Fatal(err)
			}
			raw.checkpoint.Store(true)
			buffer := make([]byte, 1024)
			count, err := client.Read(buffer)
			raw.checkpoint.Store(false)
			var temporary net.Error
			if count != 0 || !errors.As(err, &temporary) || !temporary.Temporary() {
				t.Fatal("partial actual TLS record was not retained as retryable", count, err)
			}
			close(remainder)
			if err = <-serverDone; err != nil {
				t.Fatal(err)
			}
			if err = raw.socket.wait(); err != nil {
				t.Fatal(err)
			}
			raw.checkpoint.Store(true)
			count, err = client.Read(buffer)
			raw.checkpoint.Store(false)
			if err != nil && err != io.EOF || string(buffer[:count]) != "HTTP/1.1 200 OK\r\ncontent-length: 4\r\n\r\nbody" {
				t.Fatal("actual TLS record lost bytes across native temporary read", string(buffer[:count]), err)
			}
			client.Close()
		})
	}
}
