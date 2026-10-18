package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"sync"
	"time"
)

// OwnerNativeEvent observes real owner phases; it never controls native readiness.
type OwnerNativeEvent struct {
	Kind     string
	Status   int
	Err      error
	Resource *NativeOwnerResourceState
}

// OwnerNativeIO is a finite fresh direct-HTTP1 adapter. The original owner builds
// the request, arms the timer, parses response metadata and clears it. Pure native
// workers own only dial/write/readiness; temporary awaits never own the body.
// Authenticated/HTTPS proxies, pooling, informational/upgrade/redirect and simultaneous physical
// errors/header facts remain explicit capability boundaries. This does not
// establish general Bun event-loop equivalence or full SDK/Next migration.
// NativeOwnerOperation contains physical facts and temporary readiness only.
type NativeOwnerOperation interface {
	Checkpoint() ([]byte, bool, error)
	WaitReadable(context.Context) error
}
type NativeOwnerTimer interface{ Clear() error }

// NativeOwnerBoundary must be a real owner bridge. There is no default or
// identity implementation; provider does not construct a second scheduler.
type NativeOwnerBoundary interface {
	CheckOwner() error
	ArmFetchTimer(time.Duration, func()) (NativeOwnerTimer, error)
	AwaitHeaders(NativeOwnerOperation) ([]byte, error)
	RejectFetch(error) error
	IsAdministrative(error) bool
}
type OwnerNativeIO struct {
	boundary  NativeOwnerBoundary
	observe   func(OwnerNativeEvent)
	pool      *NativeHTTP1Pool
	poolReuse bool
}

// CheckNativeOwnerBoundary checks the real host scope without constructing a
// client or a native bridge. Administrative owner failures remain distinguishable
// from SDK caller abort and fetch timeout errors.
func CheckNativeOwnerBoundary(boundary NativeOwnerBoundary) error {
	if boundary == nil {
		return fmt.Errorf("%w: explicit original owner bridge required", ErrHTTPExchangeProfile)
	}
	if err := boundary.CheckOwner(); err != nil {
		return &ownerWaitFailure{err}
	}
	return nil
}

func NewOwnerNativeIO(boundary NativeOwnerBoundary, observe func(OwnerNativeEvent)) (*OwnerNativeIO, error) {
	if err := CheckNativeOwnerBoundary(boundary); err != nil {
		return nil, err
	}
	return &OwnerNativeIO{boundary: boundary, observe: observe}, nil
}

func NewPooledOwnerNativeIO(boundary NativeOwnerBoundary, pool *NativeHTTP1Pool, observe func(OwnerNativeEvent)) (*OwnerNativeIO, error) {
	driver, err := NewOwnerNativeIO(boundary, observe)
	if err != nil {
		return nil, err
	}
	if pool == nil {
		return nil, fmt.Errorf("%w: explicit shared native HTTP pool", ErrHTTPExchangeProfile)
	}
	driver.pool = pool
	driver.poolReuse = pool.keepAlive()
	return driver, nil
}

// CaptureNativeFetchOptions is called at the actual getAnthropicClient ARGS
// producer, after its async auth/helper consumers and before provider branches.
func (driver *OwnerNativeIO) CaptureNativeFetchOptions() error {
	if err := driver.CheckOwner(); err != nil {
		return err
	}
	if driver.pool != nil {
		driver.poolReuse = driver.pool.keepAlive()
	}
	return nil
}

// CheckOwner validates the currently captured original Scope before client
// preparation. A driver retained from a previous ASK is not a fresh capability.
func (driver *OwnerNativeIO) CheckOwner() error {
	if driver == nil {
		return fmt.Errorf("%w: original native owner driver", ErrHTTPExchangeProfile)
	}
	if err := driver.boundary.CheckOwner(); err != nil {
		return &ownerWaitFailure{err}
	}
	return nil
}

func (driver *OwnerNativeIO) event(kind string, status int, err error) {
	if driver.observe != nil {
		driver.observe(OwnerNativeEvent{Kind: kind, Status: status, Err: err})
	}
}

// OpenFetch is the underlying fetch producer for operations such as OAuth
// refresh. Those calls have no Anthropic SDK fetch timeout; their caller owns
// the original signal and the returned physical response lifetime.
func (driver *OwnerNativeIO) OpenFetch(request *http.Request, exchange *RawHTTP1Exchange) (*HTTPExchangeResponse, error) {
	return driver.OpenFetchWithOptions(request, exchange, NativeFetchOptions{})
}
func (driver *OwnerNativeIO) open(caller, ctx context.Context, request *http.Request, config AnthropicConfig, delay time.Duration) (*HTTPExchangeResponse, error) {
	return driver.openResponse(caller, ctx, request, config, delay, false)
}
func (driver *OwnerNativeIO) openResponse(caller, ctx context.Context, request *http.Request, config AnthropicConfig, delay time.Duration, fetchResponse bool) (*HTTPExchangeResponse, error) {
	if err := driver.boundary.CheckOwner(); err != nil {
		return nil, &ownerWaitFailure{err}
	}
	exchange, ok := config.HTTPExchange.(*RawHTTP1Exchange)
	if !ok || exchange == nil || exchange.DialContext != nil || config.OwnerOpenIO != nil || config.HTTPClient != nil || runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("%w: owner native fresh Darwin HTTP1 without custom transport/pooling", ErrHTTPExchangeProfile)
	}
	if request.URL.Scheme != "http" && request.URL.Scheme != "https" || request.URL.User != nil || request.Header.Get("Upgrade") != "" || request.Header.Get("Proxy-Authorization") != "" {
		return nil, fmt.Errorf("%w: owner native direct HTTP1 request", ErrHTTPExchangeProfile)
	}
	if request.URL.Scheme == "http" && (exchange.VerifiedTLS != nil || exchange.ResolveVerifiedTLS != nil) {
		// Source fetch receives the process TLS options for both protocols.
		// A plaintext request does not consume or validate TLS trust, as with
		// RawHTTP1Exchange.Do and the native fetch redirect producer.
		selected := *exchange
		selected.VerifiedTLS, selected.ResolveVerifiedTLS = nil, nil
		exchange = &selected
	}
	limit := exchange.MaxHeaderBytes
	if limit == 0 {
		limit = 64 * 1024
	}
	if limit < 4 || limit > 1024*1024 {
		return nil, fmt.Errorf("%w: owner native header bound", ErrHTTPExchangeProfile)
	}
	var wire bytes.Buffer
	request = nativeFetchRequest(request)
	write := request.Write
	if exchange.ProxyURL != "" && request.URL.Scheme == "http" {
		write = request.WriteProxy
	}
	if err := write(&wire); err != nil {
		return nil, err
	}
	var resource *NativeHTTP1Reservation
	var lease *nativeOwnerBody
	var err error
	adopted := false
	clock := config.nativeFetchClock
	ownedClock := clock == nil
	if ownedClock {
		clock, err = driver.armFetchClock(delay, nil)
		if err != nil {
			return nil, err
		}
	}
	// The ticket is armed before even the known dial/write resource starts.
	defer func() {
		if ownedClock {
			clock.clear()
		}
		if lease != nil && !adopted {
			_ = lease.Close()
			state, _ := InspectNativeOwnerBody(lease)
			if driver.observe != nil {
				driver.observe(OwnerNativeEvent{Kind: "failed-lease-closed", Resource: &state})
			}
		}
	}()
	if request.URL.Scheme == "https" {
		input := exchange.VerifiedTLS
		if exchange.ResolveVerifiedTLS != nil {
			if input != nil {
				return nil, fmt.Errorf("%w: ambiguous TLS trust owner", ErrHTTPExchangeProfile)
			}
			input, err = exchange.ResolveVerifiedTLS()
			if err != nil {
				failure := originalTLSConfigurationFault(err, request.URL.String())
				if errors.Is(failure, ErrHTTPExchangeProfile) {
					return nil, failure
				}
				// Original malformed CA is an asynchronous native fetch rejection,
				// not a synchronous SDK throw. Resume on this same owner so true
				// overdue policy can run before the original fetch finally clears.
				failure = driver.boundary.RejectFetch(failure)
				driver.event("fetch-rejected", 0, failure)
				if driver.boundary.IsAdministrative(failure) {
					return nil, &ownerWaitFailure{failure}
				}
				if caller.Err() != nil {
					return nil, errors.New("Request was aborted.")
				}
				if clock.timedOut {
					return nil, errors.New("Request timed out.")
				}
				return nil, nativeOwnerConnectionFailure(failure)
			}
		}
		verified, failure := verifiedHTTP1TLS(input, request.URL.Hostname())
		if failure != nil {
			return nil, failure
		}
		if driver.pool != nil {
			resource, err = driver.pool.reserve(ctx, nativeHTTP1PoolKey{scheme: "https", authority: nativeTargetAuthority(request.URL), proxy: exchange.ProxyURL, trust: verified, tlsOptions: exchange.NativeTLSOptions}, wire.Bytes(), limit, driver.poolReuse)
		} else {
			resource, err = newNativeTLSHTTP1ProxyReservation(ctx, nativeTargetAuthority(request.URL), wire.Bytes(), limit, verified, exchange.ProxyURL)
		}
	} else {
		authority := nativeTargetAuthority(request.URL)
		if exchange.ProxyURL != "" {
			authority, err = nativeProxyAuthority(exchange.ProxyURL)
			if err != nil {
				return nil, err
			}
		}
		if driver.pool != nil {
			resource, err = driver.pool.reserve(ctx, nativeHTTP1PoolKey{scheme: "http", authority: nativeTargetAuthority(request.URL), proxy: exchange.ProxyURL}, wire.Bytes(), limit, driver.poolReuse)
		} else {
			resource, err = NewNativeHTTP1Reservation(ctx, authority, wire.Bytes(), limit)
		}
	}
	if err != nil {
		return nil, err
	}
	clock.resource = resource
	if clock.timedOut {
		_ = resource.Close()
	}
	physicalBody := &nativeBodyReadLease{resource: resource}
	lease = &nativeOwnerBody{resource: resource, physical: physicalBody, driver: driver}
	callbackDone := make(chan struct{})
	lease.stopParent = context.AfterFunc(ctx, func() { physicalBody.closePhysical(); close(callbackDone) })
	lease.callbackDone = callbackDone

	driver.event("fetch-enter", 0, nil)
	raw, err := driver.boundary.AwaitHeaders(resource)
	if err != nil {
		driver.event("fetch-rejected", 0, err)
		if errors.Is(err, ErrHTTPExchangeProfile) {
			return nil, err
		}
		if driver.boundary.IsAdministrative(err) {
			return nil, &ownerWaitFailure{err}
		}
		if caller.Err() != nil {
			return nil, errors.New("Request was aborted.")
		}
		if clock.timedOut {
			return nil, errors.New("Request timed out.")
		}

		if request.URL.Scheme == "https" {
			if fault, matched := resource.verifiedTLSHeaderClose(err, request.URL.String()); matched {
				err = fault
			} else {
				err = originalTLSFault(err, request.URL.String())
			}
			if errors.Is(err, ErrHTTPExchangeProfile) {
				return nil, err
			}
		}
		return nil, nativeOwnerConnectionFailure(err)
	}
	response, err := resource.AdoptResponse(request) // same accumulated header/body bytes
	if err != nil {
		// A real caller may close the native reservation after its complete header
		// receipt but before Response adoption. This is still the SDK fetch/create
		// handoff, so the original aborted signal selects APIUserAbortError. Keep
		// unrelated framing/profile faults and administrative owner failures intact.
		if errors.Is(err, net.ErrClosed) {
			if caller.Err() != nil {
				return nil, errors.New("Request was aborted.")
			}
			if clock.timedOut {
				return nil, errors.New("Request timed out.")
			}
		}

		return nil, fmt.Errorf("%w: original HTTP framing: %v", ErrHTTPExchangeProfile, err)
	}
	var original *BunHeaders
	if fetchResponse {
		original, err = nativeFetchHeaders(raw, response.StatusCode)
	} else {
		original, err = nativeOwnerHeaders(raw, response.StatusCode)
	}
	if err != nil {
		return nil, err
	}
	response.Header = original.HTTPHeader()
	decoded := nativeFetchBody(response.Body, response.Header.Get("Content-Encoding"), request.URL.String())
	lease.ReadCloser = decoded
	physicalBody.body = decoded
	if decoded != response.Body {
		physicalBody.decoder = decoded
	}
	response.Body = lease
	driver.event("fetch-fulfilled", response.StatusCode, nil)
	adopted = true
	return &HTTPExchangeResponse{Response: response, OriginalHeaders: original}, nil
}

func nativeOwnerConnectionFailure(err error) error {
	return &SDKStreamError{Kind: "request_connection", Class: "APIConnectionError", Name: "Error", Message: "Connection error.", MessageJSON: []byte(`"Connection error."`), Type: json.RawMessage(`null`), Cause: err}
}

// nativeOwnerHeaders preserves the original physical rows for the existing Bun
// Headers projection. http.Header iteration is never its source of metadata.
func nativeOwnerHeaders(raw []byte, status int) (*BunHeaders, error) {
	if status < 200 || status >= 300 && status < 400 {
		return nil, fmt.Errorf("%w: informational/upgrade/redirect response", ErrHTTPExchangeProfile)
	}
	return nativeOwnerHeaderRows(raw)
}
func nativeOwnerHeaderRows(raw []byte) (*BunHeaders, error) {
	if len(raw) < 4 || !bytes.HasSuffix(raw, []byte("\r\n\r\n")) {
		return nil, fmt.Errorf("%w: incomplete original header snapshot", ErrHTTPExchangeProfile)
	}
	var pairs [][2][]byte
	for _, line := range bytes.Split(raw[:len(raw)-4], []byte("\r\n"))[1:] {
		if len(line) == 0 || line[0] == ' ' || line[0] == '\t' {
			return nil, fmt.Errorf("%w: obsolete header folding", ErrHTTPExchangeProfile)
		}
		colon := bytes.IndexByte(line, ':')
		if colon <= 0 {
			return nil, fmt.Errorf("%w: original header syntax", ErrHTTPExchangeProfile)
		}
		pairs = append(pairs, [2][]byte{line[:colon], bytes.Trim(line[colon+1:], " \t")})
	}
	pairsDecoded, err := HeaderPairsFromWire(pairs)
	if err != nil {
		return nil, err
	}
	original, err := NewFetchHeaders(pairsDecoded)
	if err != nil {
		return nil, err
	}
	return original, nil
}

// Successful adoption transfers this lease. Close joins native dial/write and
// readiness demands, and stops/joins caller cancellation without touching owner.
type nativeOwnerBody struct {
	io.ReadCloser
	resource     *NativeHTTP1Reservation
	physical     *nativeBodyReadLease
	driver       *OwnerNativeIO
	stopParent   func() bool
	callbackDone chan struct{}
	traceBranch  *responseBranch
	once         sync.Once
	err          error
}

func (body *nativeOwnerBody) Close() error {
	body.once.Do(func() {
		if body.traceBranch != nil {
			body.err = body.traceBranch.Close()
		} else {
			body.err = body.closeAll()
		}
	})
	return body.err
}

func (body *nativeOwnerBody) closeAll() error {
	if body.stopParent != nil && !body.stopParent() {
		<-body.callbackDone
	}
	if body.physical != nil {
		return body.physical.Close()
	}
	return body.resource.Close()
}

// NativeOwnerResourceState reports physical lifetime only, never admission.
type NativeOwnerResourceState struct{ Closed, Joined, OperationClosed, PendingReadiness bool }

func InspectNativeOwnerBody(body io.ReadCloser) (NativeOwnerResourceState, error) {
	lease, ok := body.(*nativeOwnerBody)
	if !ok {
		return NativeOwnerResourceState{}, fmt.Errorf("%w: native original body lease", ErrHTTPExchangeProfile)
	}
	lease.resource.mu.Lock()
	operation, closed := lease.resource.operation, lease.resource.closed
	lease.resource.mu.Unlock()
	state := NativeOwnerResourceState{Closed: closed}
	select {
	case <-lease.resource.done:
		state.Joined = true
	default:
	}
	if operation != nil {
		operation.mu.Lock()
		state.OperationClosed = operation.closed
		state.PendingReadiness = operation.waiting
		operation.mu.Unlock()
	}
	return state, nil
}
