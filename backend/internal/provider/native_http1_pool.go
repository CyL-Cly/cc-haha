package provider

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
)

// NativeHTTP1Pool retains physical connections, never a Scope, request,
// authorization header or SDK policy. Its host binding proves the one Owner
// separately on every request. Busy connections are never multiplexed.
type NativeHTTP1Pool struct {
	mu                sync.Mutex
	life              context.Context
	cancel            context.CancelFunc
	closed            bool
	idle              []nativeIdleHTTP1
	active            map[*NativeHTTP1Reservation]bool
	keepAliveDisabled bool
}

type nativeHTTP1PoolKey struct {
	scheme, authority, proxy string
	trust                    *tls.Config
	tlsOptions               bool
}
type nativeIdleHTTP1 struct {
	key       nativeHTTP1PoolKey
	operation *NativeHTTP1Operation
}

func NewNativeHTTP1Pool() *NativeHTTP1Pool {
	life, cancel := context.WithCancel(context.Background())
	return &NativeHTTP1Pool{life: life, cancel: cancel, active: map[*NativeHTTP1Reservation]bool{}}
}

func sameNativeHTTP1PoolKey(a, b nativeHTTP1PoolKey) bool {
	if a.scheme != b.scheme || a.authority != b.authority || a.proxy != b.proxy || a.tlsOptions != b.tlsOptions {
		return false
	}
	if a.trust == nil || b.trust == nil {
		return a.trust == nil && b.trust == nil
	}
	// verifiedHTTP1TLS supplies explicit roots and prohibits caller verification
	// callbacks/client certificates. Distinct trust arrays must not borrow a
	// socket authenticated under a previous configuration.
	return a.trust.RootCAs.Equal(b.trust.RootCAs) && a.trust.ServerName == b.trust.ServerName &&
		a.trust.MinVersion == b.trust.MinVersion && a.trust.MaxVersion == b.trust.MaxVersion
}

func (pool *NativeHTTP1Pool) keepAlive() bool {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	return !pool.keepAliveDisabled
}

// Source proxy.disableKeepAlive is sticky and affects newly constructed SDK
// fetch options. It does not rewrite an existing client's captured options or
// the init HEAD's default fetch options.
func (pool *NativeHTTP1Pool) DisableKeepAlive() {
	pool.mu.Lock()
	pool.keepAliveDisabled = true
	pool.mu.Unlock()
}

func (pool *NativeHTTP1Pool) reserve(ctx context.Context, key nativeHTTP1PoolKey, wire []byte, limit int, reuse bool) (*NativeHTTP1Reservation, error) {
	if pool == nil || ctx == nil || len(wire) == 0 || len(wire) > 8*1024*1024 || limit < 4 || limit > 1024*1024 {
		return nil, fmt.Errorf("%w: finite pooled response reservation", ErrHTTPExchangeProfile)
	}
	if _, _, err := net.SplitHostPort(key.authority); err != nil {
		return nil, fmt.Errorf("%w: pooled TCP authority", ErrHTTPExchangeProfile)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var operation *NativeHTTP1Operation
	pool.mu.Lock()
	if pool.closed {
		pool.mu.Unlock()
		return nil, net.ErrClosed
	}
	for i := len(pool.idle) - 1; i >= 0; i-- {
		if !reuse {
			break
		}
		entry := pool.idle[i]
		if !sameNativeHTTP1PoolKey(entry.key, key) {
			continue
		}
		pool.idle = append(pool.idle[:i], pool.idle[i+1:]...)
		operation = entry.operation
		break
	}
	work, cancel := context.WithCancel(ctx)
	reservation := &NativeHTTP1Reservation{available: make(chan struct{}), done: make(chan struct{}), cancel: cancel, pool: pool, poolKey: key, poolReuse: reuse}
	pool.active[reservation] = true
	pool.mu.Unlock()
	request := append([]byte(nil), wire...)
	if operation != nil {
		// A peer FIN or unsolicited bytes observed before this request retires
		// the idle resource. This probe never sends or retries request bytes.
		var scratch [1]byte
		count, failure := operation.readNativeBytes(scratch[:])
		if count != 0 || failure != nil {
			_ = operation.Close()
			operation = nil
		}
	}
	if operation != nil {
		operation.mu.Lock()
		operation.bytes, operation.position, operation.headerEnd = nil, 0, 0
		operation.adopted = false
		operation.reader = nil
		operation.limit = limit
		operation.mu.Unlock()
		go reservation.reuseAndWrite(work, operation, request)
	} else if key.trust != nil {
		go reservation.dialTLSAndWrite(work, key.authority, request, limit, key.trust, key.proxy)
	} else {
		destination := key.authority
		if key.proxy != "" {
			var err error
			destination, err = nativeProxyAuthority(key.proxy)
			if err != nil {
				reservation.publishFailure(err)
				close(reservation.done)
				return reservation, nil
			}
		}
		go reservation.dialAndWrite(work, destination, request, limit)
	}
	return reservation, nil
}

func (reservation *NativeHTTP1Reservation) reuseAndWrite(ctx context.Context, operation *NativeHTTP1Operation, wire []byte) {
	defer close(reservation.done)
	reservation.mu.Lock()
	if reservation.closed {
		reservation.mu.Unlock()
		_ = operation.Close()
		return
	}
	reservation.operation = operation
	reservation.mu.Unlock()
	reservation.announce()
	var count int
	var err error
	if stream, tls := operation.recordReader.(*nativeTLSHTTP1Stream); tls {
		count, err = stream.secured.Write(wire)
		if err == nil {
			err = stream.wrapped.writer.drain(ctx)
		}
	} else {
		count, err = operation.connection.Write(wire)
	}
	if err == nil && count != len(wire) {
		err = io.ErrShortWrite
	}
	if err != nil {
		reservation.publishFailure(err)
		operation.socket.wake()
	}
}

func (pool *NativeHTTP1Pool) release(reservation *NativeHTTP1Reservation, operation *NativeHTTP1Operation, eligible bool) bool {
	if operation == nil {
		pool.mu.Lock()
		delete(pool.active, reservation)
		pool.mu.Unlock()
		return false
	}
	operation.readMu.Lock()
	defer operation.readMu.Unlock()
	operation.mu.Lock()
	eligible = eligible && !operation.closed && !operation.waiting && operation.terminal == nil && operation.position == len(operation.bytes) &&
		operation.reader != nil && operation.reader.Buffered() == 0
	operation.mu.Unlock()
	pool.mu.Lock()
	defer pool.mu.Unlock()
	delete(pool.active, reservation)
	if pool.closed || !eligible {
		return false
	}
	pool.idle = append(pool.idle, nativeIdleHTTP1{reservation.poolKey, operation})
	return true
}

// Close owns active dial/write and idle transport resources. No worker waits
// for the Owner, so global shutdown can cancel and join all of them.
func (pool *NativeHTTP1Pool) Close() error {
	if pool == nil {
		return nil
	}
	pool.mu.Lock()
	if pool.closed {
		pool.mu.Unlock()
		return nil
	}
	pool.closed = true
	pool.cancel()
	idle := pool.idle
	pool.idle = nil
	active := make([]*NativeHTTP1Reservation, 0, len(pool.active))
	for reservation := range pool.active {
		active = append(active, reservation)
	}
	pool.mu.Unlock()
	for _, reservation := range active {
		_ = reservation.Close()
	}
	for _, entry := range idle {
		_ = entry.operation.Close()
	}
	return nil
}

// Head is the native-only init transport. It has no SDK/Owner callbacks; the
// init producer owns its existing detached completion and ten-second signal.
// Response framing and the physical reservation are identical to model fetch.
func (pool *NativeHTTP1Pool) Head(request *http.Request, exchange *RawHTTP1Exchange) (*http.Response, error) {
	return pool.HeadInitialized(request, exchange, nil)
}

func (pool *NativeHTTP1Pool) HeadInitialized(request *http.Request, exchange *RawHTTP1Exchange, initialization *NativeHTTP1HeadInitialization) (*http.Response, error) {
	if request == nil || request.Method != http.MethodHead || request.URL == nil || request.URL.User != nil || exchange == nil || exchange.DialContext != nil {
		return nil, fmt.Errorf("%w: init HEAD shared native transport", ErrHTTPExchangeProfile)
	}
	key := nativeHTTP1PoolKey{scheme: request.URL.Scheme, authority: nativeTargetAuthority(request.URL), proxy: exchange.ProxyURL}
	if key.scheme != "http" && key.scheme != "https" {
		return nil, fmt.Errorf("%w: init HEAD protocol", ErrHTTPExchangeProfile)
	}
	if key.scheme == "https" {
		input := exchange.VerifiedTLS
		if exchange.ResolveVerifiedTLS != nil {
			if input != nil {
				return nil, fmt.Errorf("%w: ambiguous init TLS trust", ErrHTTPExchangeProfile)
			}
			var err error
			input, err = exchange.ResolveVerifiedTLS()
			if err != nil {
				return nil, err
			}
		}
		var err error
		key.trust, err = verifiedHTTP1TLS(input, request.URL.Hostname())
		if err != nil {
			return nil, err
		}
	}
	request = nativeFetchRequest(request)
	var wire bytes.Buffer
	write := request.Write
	if key.proxy != "" && key.scheme == "http" {
		write = request.WriteProxy
	}
	if err := write(&wire); err != nil {
		return nil, err
	}
	limit := exchange.MaxHeaderBytes
	if limit == 0 {
		limit = 64 * 1024
	}
	reservation, err := pool.reserve(request.Context(), key, wire.Bytes(), limit, true)
	if err != nil {
		return nil, err
	}
	initialization.admitted()
	stop := context.AfterFunc(request.Context(), func() { _ = reservation.Close() })
	defer stop()
	for {
		_, ready, failure := reservation.Checkpoint()
		if failure != nil {
			_ = reservation.Close()
			return nil, failure
		}
		if ready {
			response, failure := reservation.AdoptResponse(request)
			if failure != nil {
				_ = reservation.Close()
			}
			return response, failure
		}
		if failure = reservation.WaitReadable(request.Context()); failure != nil {
			_ = reservation.Close()
			return nil, failure
		}
	}
}
