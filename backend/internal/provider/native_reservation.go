package provider

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
)

// NativeHTTP1Reservation is known before its pure dial/write worker starts.
// The response descriptor is published BEFORE the first request byte is sent;
// physical response readiness never depends on a post-write completion message.
// This is a finite direct-TCP resource, not HTTP policy, Headers projection or
// an SDK transport. TLS, proxy/pool and error/ready ties need separate evidence.
type NativeHTTP1Reservation struct {
	mu        sync.Mutex
	operation *NativeHTTP1Operation
	failure   error
	closed    bool
	available chan struct{}
	announced sync.Once
	done      chan struct{}
	cancel    context.CancelFunc
	closeOnce sync.Once
	closeErr  error
}

func NewNativeHTTP1Reservation(ctx context.Context, authority string, wire []byte, headerLimit int) (*NativeHTTP1Reservation, error) {
	if ctx == nil || len(wire) == 0 || len(wire) > 8*1024*1024 || headerLimit < 4 || headerLimit > 1024*1024 {
		return nil, fmt.Errorf("%w: invalid finite response reservation", ErrHTTPExchangeProfile)
	}
	if _, _, err := net.SplitHostPort(authority); err != nil {
		return nil, fmt.Errorf("%w: response reservation requires TCP authority", ErrHTTPExchangeProfile)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	work, cancel := context.WithCancel(ctx)
	reservation := &NativeHTTP1Reservation{available: make(chan struct{}), done: make(chan struct{}), cancel: cancel}
	request := append([]byte(nil), wire...)
	go reservation.dialAndWrite(work, authority, request, headerLimit)
	return reservation, nil
}

// This worker owns only network resources and copied request bytes. It cannot
// read response bytes, classify status, invoke owner policy or mutate messages.
func (reservation *NativeHTTP1Reservation) dialAndWrite(ctx context.Context, authority string, wire []byte, limit int) {
	defer close(reservation.done)
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", authority)
	if err != nil {
		reservation.publishFailure(err)
		return
	}
	// net.Dialer with network tcp returns TCPConn. Reserve the native descriptor
	// directly; the existing public operation API assumes an already sent request.
	tcp := connection.(*net.TCPConn)
	socket, err := newNativeHTTP1Socket(tcp)
	if err != nil {
		_ = connection.Close()
		reservation.publishFailure(err)
		return
	}
	operation := &NativeHTTP1Operation{connection: tcp, socket: socket, limit: limit}
	reservation.mu.Lock()
	if reservation.closed {
		reservation.mu.Unlock()
		_ = operation.Close()
		return
	}
	reservation.operation = operation
	reservation.mu.Unlock()
	reservation.announce()
	// The owner can checkpoint this exact descriptor even while Write returns
	// or before this goroutine publishes its terminal completion.
	count, err := connection.Write(wire)
	if err == nil && count != len(wire) {
		err = io.ErrShortWrite
	}
	if err != nil {
		reservation.publishFailure(err)
		operation.socket.wake()
	}
}

func (reservation *NativeHTTP1Reservation) announce() {
	reservation.announced.Do(func() { close(reservation.available) })
}

func (reservation *NativeHTTP1Reservation) publishFailure(err error) {
	reservation.mu.Lock()
	reservation.failure = err
	reservation.mu.Unlock()
	reservation.announce()
}

// Checkpoint runs only on the owner. Native bytes win over a delayed successful
// Write acknowledgement. A simultaneous physical write fault and full headers
// is explicitly unsupported until the Source policy for that tie is measured.
func (reservation *NativeHTTP1Reservation) Checkpoint() ([]byte, bool, error) {
	reservation.mu.Lock()
	operation, failure, closed := reservation.operation, reservation.failure, reservation.closed
	reservation.mu.Unlock()
	if closed {
		return nil, false, net.ErrClosed
	}
	if operation == nil {
		return nil, false, failure
	}
	checkpoint := operation.Checkpoint()
	// Re-observe physical writer/close facts after polling. This closes a stale
	// snapshot window; it is not a proof of every future write-error/header tie.
	reservation.mu.Lock()
	failure, closed = reservation.failure, reservation.closed
	reservation.mu.Unlock()
	if closed {
		return nil, false, net.ErrClosed
	}
	if checkpoint.State == NativeHTTP1HeaderComplete {
		if failure != nil {
			return nil, false, fmt.Errorf("%w: full headers overlap write failure", ErrHTTPExchangeProfile)
		}
		return checkpoint.Header, true, nil
	}
	if failure != nil {
		return nil, false, failure
	}
	return nil, false, checkpoint.Err
}

// WaitReadable waits for resource publication or native readiness. Cancelling
// this temporary demand neither owns nor closes the request/body resource.
func (reservation *NativeHTTP1Reservation) WaitReadable(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for {
		reservation.mu.Lock()
		operation, failure, closed := reservation.operation, reservation.failure, reservation.closed
		reservation.mu.Unlock()
		if closed {
			return net.ErrClosed
		}
		if failure != nil {
			return failure
		}
		if operation != nil {
			return operation.WaitReadable(ctx)
		}
		select {
		case <-reservation.available:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (reservation *NativeHTTP1Reservation) AdoptResponse(request *http.Request) (*http.Response, error) {
	reservation.mu.Lock()
	operation, closed := reservation.operation, reservation.closed
	reservation.mu.Unlock()
	if closed {
		return nil, net.ErrClosed
	}
	if operation == nil {
		return nil, fmt.Errorf("%w: response resource not published", ErrHTTPExchangeProfile)
	}
	response, err := operation.AdoptResponse(request)
	if err != nil {
		return nil, err
	}
	response.Body = &nativeReservedBody{ReadCloser: response.Body, reservation: reservation}
	return response, nil
}

// Close cancels dial/write, closes original and duplicate descriptors, and joins
// pure workers. It may run inside a timer callback: no worker awaits that owner.
func (reservation *NativeHTTP1Reservation) Close() error {
	reservation.closeOnce.Do(func() {
		reservation.cancel()
		reservation.mu.Lock()
		reservation.closed = true
		operation := reservation.operation
		reservation.mu.Unlock()
		reservation.announce()
		if operation != nil {
			reservation.closeErr = operation.Close()
		}
		<-reservation.done
	})
	return reservation.closeErr
}

type nativeReservedBody struct {
	io.ReadCloser
	reservation *NativeHTTP1Reservation
}

func (body *nativeReservedBody) Close() error { return body.reservation.Close() }

var _ io.Closer = (*NativeHTTP1Reservation)(nil)
