package provider

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
)

// Handshake is a pure, cancellation-owned transport worker. Go caches handshake
// errors, so it is never fake-resumed after EAGAIN. After verified handshake,
// the exact tls.Conn switches to genuine nonblocking native record reads and is
// published BEFORE the first HTTP request byte. The owner can decode currently
// available TLS records without waiting for a post-write worker receipt.
// A handshake-ready/due policy tie before publication is not proved here.
func newNativeTLSHTTP1Reservation(ctx context.Context, authority string, wire []byte, limit int, verified *tls.Config) (*NativeHTTP1Reservation, error) {
	return newNativeTLSHTTP1ProxyReservation(ctx, authority, wire, limit, verified, "")
}

func newNativeTLSHTTP1ProxyReservation(ctx context.Context, authority string, wire []byte, limit int, verified *tls.Config, proxyURL string) (*NativeHTTP1Reservation, error) {
	if ctx == nil || len(wire) == 0 || len(wire) > 8*1024*1024 || limit < 4 || limit > 1024*1024 || verified == nil {
		return nil, fmt.Errorf("%w: finite verified TLS response reservation", ErrHTTPExchangeProfile)
	}
	if _, _, err := net.SplitHostPort(authority); err != nil {
		return nil, fmt.Errorf("%w: TLS response reservation requires TCP authority", ErrHTTPExchangeProfile)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if proxyURL != "" {
		if _, err := nativeProxyAuthority(proxyURL); err != nil {
			return nil, err
		}
	}
	work, cancel := context.WithCancel(ctx)
	reservation := &NativeHTTP1Reservation{available: make(chan struct{}), done: make(chan struct{}), cancel: cancel}
	request := append([]byte(nil), wire...)
	config := verified.Clone()
	config.RootCAs = verified.RootCAs.Clone()
	go reservation.dialTLSAndWrite(work, authority, request, limit, config, proxyURL)
	return reservation, nil
}
func (reservation *NativeHTTP1Reservation) dialTLSAndWrite(ctx context.Context, authority string, wire []byte, limit int, config *tls.Config, proxyURL string) {
	defer close(reservation.done)
	destination := authority
	if proxyURL != "" {
		destination, _ = nativeProxyAuthority(proxyURL)
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", destination)
	if err != nil {
		reservation.publishFailure(err)
		return
	}
	if proxyURL != "" {
		if err := negotiateNativeProxy(ctx, connection, authority, limit); err != nil {
			_ = connection.Close()
			reservation.publishFailure(err)
			return
		}
	}
	tcp := connection.(*net.TCPConn)
	socket, err := newNativeHTTP1Socket(tcp)
	if err != nil {
		connection.Close()
		reservation.publishFailure(err)
		return
	}
	wrapped := &nativeTLSConn{Conn: tcp, socket: socket}
	secured := tls.Client(wrapped, config)
	if err = secured.HandshakeContext(ctx); err != nil {
		// Handshake cancellation closes original net.Conn; the duplicated FD still
		// needs explicit shutdown/close before publishing failure/join completion.
		socket.shutdown()
		connection.Close()
		socket.close()
		reservation.publishFailure(err)
		return
	}
	writer, err := newNativeTLSRecordWriter(ctx, socket, func(failure error) {
		wrapped.writeFault.Store(&nativeTLSWriteFault{err: failure})
		reservation.publishFailure(failure)
		socket.wake()
	})
	if err != nil {
		socket.shutdown()
		connection.Close()
		socket.close()
		reservation.publishFailure(err)
		return
	}
	wrapped.writer = writer
	wrapped.native.Store(true)
	stream := &nativeTLSHTTP1Stream{secured: secured, wrapped: wrapped}
	operation := &NativeHTTP1Operation{connection: tcp, socket: socket, recordReader: stream, limit: limit}
	reservation.mu.Lock()
	if reservation.closed {
		reservation.mu.Unlock()
		operation.Close()
		return
	}
	reservation.operation = operation
	reservation.mu.Unlock()
	reservation.announce()
	count, err := secured.Write(wire)
	if err == nil && count != len(wire) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = writer.drain(ctx)
	}
	if err != nil {
		reservation.publishFailure(err)
		socket.wake()
	}
}

// This temporary native no-byte fact retains crypto/tls.rawInput, including a
// partial record header/payload, rather than poisoning a completed handshake.
type nativeTLSWouldBlock struct{}

func (*nativeTLSWouldBlock) Error() string   { return "native TLS record has no readable bytes" }
func (*nativeTLSWouldBlock) Temporary() bool { return true }
func (*nativeTLSWouldBlock) Timeout() bool   { return false }

type nativeTLSConn struct {
	net.Conn
	socket     *nativeHTTP1Socket
	native     atomic.Bool
	writeFault atomic.Pointer[nativeTLSWriteFault]
	writer     *nativeTLSRecordWriter
}
type nativeTLSWriteFault struct{ err error }

func (connection *nativeTLSConn) Read(out []byte) (int, error) {
	if !connection.native.Load() {
		return connection.Conn.Read(out)
	}
	count, err := connection.socket.read(out)
	if count == 0 && err == nil {
		return 0, &nativeTLSWouldBlock{}
	}
	return count, err
}
func (connection *nativeTLSConn) Write(out []byte) (int, error) {
	if !connection.native.Load() {
		return connection.Conn.Write(out)
	}
	var count int
	var err error
	if connection.writer != nil {
		count, err = connection.writer.enqueue(out)
	} else {
		count, err = nativeTLSWrite(connection.socket, out)
	}
	if err != nil {
		connection.writeFault.Store(&nativeTLSWriteFault{err})
	}
	return count, err
}

// TLS record decoding is bounded by crypto/tls's record/non-advancing limits.
// Any transport write during Read (e.g. alert/key-update) queues exact encrypted
// bytes on the same owned FIFO. The owner never waits for writable readiness;
// the transport worker never reads response bytes or calls owner policy.
type nativeTLSHTTP1Stream struct {
	secured *tls.Conn
	wrapped *nativeTLSConn
}

func (stream *nativeTLSHTTP1Stream) closeTransport() {
	if stream.wrapped.writer != nil {
		stream.wrapped.writer.close()
	}
}

func (stream *nativeTLSHTTP1Stream) read(out []byte) (int, error) {
	count, err := stream.secured.Read(out)
	if failure := stream.wrapped.writeFault.Load(); failure != nil {
		return count, failure.err
	}
	var temporary net.Error
	if errors.As(err, &temporary) && temporary.Temporary() {
		return count, nil
	}
	return count, err
}
