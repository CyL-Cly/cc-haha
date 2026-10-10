package provider

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

type OwnerIOWait uint8

const (
	OwnerNetworkOpen OwnerIOWait = iota
	OwnerErrorBody
	OwnerBodyRead
)

// OwnerAwait is called only from the original owner stack. Its work closure
// receives immutable plan data/private resources, never the owner or callbacks.
type OwnerAwait func(OwnerIOWait, func(context.Context) ([]byte, error)) ([]byte, error)

// Scheduler completion failure belongs to the owner. It is not a native fetch
// fault and must not become a retryable APIConnectionError or HTTP error body.
type ownerWaitFailure struct{ cause error }

func (err *ownerWaitFailure) Error() string { return err.cause.Error() }
func (err *ownerWaitFailure) Unwrap() error { return err.cause }

type OwnerOpenIO struct {
	await  OwnerAwait
	mu     sync.Mutex
	next   uint64
	leases map[uint64]*ownerLease
}

func NewOwnerOpenIO(await OwnerAwait) (*OwnerOpenIO, error) {
	if await == nil {
		return nil, errors.New("owner Open requires an explicit await")
	}
	return &OwnerOpenIO{await: await, leases: map[uint64]*ownerLease{}}, nil
}

// SendPlan has no source env, original arrays, callbacks or external request.
// Its fields are private and only read by one physical Open worker.
type SendPlan struct {
	endpoint, method string
	headers          http.Header
	body             []byte
	trust            *tls.Config
	headerLimit      int
}

// buildSendPlan runs after the existing header timer has been activated. CA
// parsing must not become an earlier GetClient/configuration failure.
func buildSendPlan(request *http.Request, exchange *RawHTTP1Exchange) (*SendPlan, error) {
	if exchange == nil || exchange.DialContext != nil || exchange.ProxyURL != "" {
		return nil, fmt.Errorf("%w: immutable direct native dial", ErrHTTPExchangeProfile)
	}
	if request == nil || request.URL == nil || request.URL.Scheme != "http" && request.URL.Scheme != "https" || request.URL.Hostname() == "" || request.URL.User != nil {
		return nil, fmt.Errorf("%w: direct HTTP/1 URL without proxy/userinfo", ErrHTTPExchangeProfile)
	}
	var verified *tls.Config
	if request.URL.Scheme == "https" {
		input := exchange.VerifiedTLS
		if exchange.ResolveVerifiedTLS != nil {
			if input != nil {
				return nil, fmt.Errorf("%w: ambiguous TLS trust owner", ErrHTTPExchangeProfile)
			}
			var err error
			input, err = exchange.ResolveVerifiedTLS() // owner only
			if err != nil {
				return nil, originalTLSConfigurationFault(err, request.URL.String())
			}
		}
		var err error
		verified, err = verifiedHTTP1TLS(input, request.URL.Hostname())
		if err != nil {
			return nil, err
		}
	}
	if request.Header.Get("Upgrade") != "" {
		return nil, fmt.Errorf("%w: protocol upgrade", ErrHTTPExchangeProfile)
	}
	limit := exchange.MaxHeaderBytes
	if limit == 0 {
		limit = 64 * 1024
	}
	if limit < 4 || limit > 1024*1024 {
		return nil, fmt.Errorf("%w: bounded original header capture", ErrHTTPExchangeProfile)
	}
	if request.GetBody == nil {
		return nil, fmt.Errorf("%w: independently repeatable signed body", ErrHTTPExchangeProfile)
	}
	body, err := request.GetBody()
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil {
		return nil, err
	}
	request = nativeFetchRequest(request)
	return &SendPlan{request.URL.String(), request.Method, request.Header.Clone(), append([]byte(nil), raw...), verified, limit}, nil
}

type ownerHead struct {
	Status  int    `json:"status"`
	Headers []byte `json:"headers"`
}

func (driver *OwnerOpenIO) reserve(ctx context.Context, timer *time.Timer) *ownerLease {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	driver.next++
	ctx, cancel := context.WithCancel(ctx)
	lease := &ownerLease{ctx: ctx, cancel: cancel, timer: timer, driver: driver, id: driver.next}
	driver.leases[lease.id] = lease
	lease.stopParent = context.AfterFunc(ctx, lease.closePhysical)
	return lease
}

// open is owner code. No response pointer crosses the AwaitBytes result: the
// reserved resource is always known before waiting and is closed if not adopted.
func (driver *OwnerOpenIO) open(request *http.Request, exchange *RawHTTP1Exchange, timer *time.Timer) (*HTTPExchangeResponse, error) {
	lease := driver.reserve(request.Context(), timer)
	adopted := false
	defer func() {
		if !adopted {
			_ = lease.Close()
		}
	}()
	plan, err := buildSendPlan(request, exchange)
	if err != nil {
		return nil, err
	}
	raw, err := driver.await(OwnerNetworkOpen, func(wait context.Context) ([]byte, error) {
		return physicalOpen(wait, lease, plan)
	})
	if err != nil {
		var native *ownerNativeFault
		if errors.As(err, &native) {
			if native.operation == "handshake" {
				return nil, originalTLSFault(native.cause, plan.endpoint)
			}
			return nil, native.cause
		}
		var profile *ownerPhysicalProfile
		if errors.As(err, &profile) {
			return nil, fmt.Errorf("%w: %s", ErrHTTPExchangeProfile, profile.boundary)
		}
		return nil, &ownerWaitFailure{err}
	}
	var head ownerHead
	if err = json.Unmarshal(raw, &head); err != nil {
		return nil, err
	}
	if len(head.Headers) < 4 {
		return nil, fmt.Errorf("%w: immutable response header snapshot", ErrHTTPExchangeProfile)
	}
	lines := bytes.Split(head.Headers[:len(head.Headers)-4], []byte("\r\n"))
	var pairs [][2][]byte
	for _, line := range lines[1:] {
		if len(line) == 0 || line[0] == ' ' || line[0] == '\t' {
			return nil, fmt.Errorf("%w: obsolete header folding", ErrHTTPExchangeProfile)
		}
		colon := bytes.IndexByte(line, ':')
		if colon <= 0 {
			return nil, fmt.Errorf("%w: original header syntax", ErrHTTPExchangeProfile)
		}
		pairs = append(pairs, [2][]byte{line[:colon], bytes.Trim(line[colon+1:], " \t")})
	}
	original, err := HeaderPairsFromWire(pairs)
	if err != nil {
		return nil, err
	}
	headers, err := NewFetchHeaders(original) // Bun projection stays owner
	if err != nil {
		return nil, err
	}
	if head.Status < 200 || head.Status >= 300 && head.Status < 400 {
		return nil, fmt.Errorf("%w: informational/upgrade/redirect response", ErrHTTPExchangeProfile)
	}
	adopted = true
	return &HTTPExchangeResponse{Response: &http.Response{StatusCode: head.Status, Header: headers.HTTPHeader(), Body: lease}, OriginalHeaders: headers}, nil
}

func (driver *OwnerOpenIO) errorBody(body io.ReadCloser) ([]byte, error) {
	lease, ok := body.(*ownerLease)
	if !ok || lease.driver != driver {
		return nil, fmt.Errorf("%w: owner error-body lease", ErrHTTPExchangeProfile)
	}
	return readOwnerErrorBody(driver.await, lease)
}

func readOwnerErrorBody(await OwnerAwait, lease ownerReadLease) ([]byte, error) {
	raw, err := await(OwnerErrorBody, func(wait context.Context) ([]byte, error) {
		if !lease.beginRead() {
			return nil, &ownerNativeFault{"errorbody", context.Canceled}
		}
		defer lease.endRead()
		stop := context.AfterFunc(wait, lease.closePhysical)
		defer stop()
		raw, err := io.ReadAll(lease)
		if err != nil {
			return raw, &ownerNativeFault{"errorbody", err}
		}
		return raw, nil
	})
	if err != nil {
		var native *ownerNativeFault
		if errors.As(err, &native) {
			return raw, native.cause
		}
		return nil, &ownerWaitFailure{err}
	}
	return raw, nil
}

// LeaseCount is a resource-only diagnostic; it never drives model policy.
func (driver *OwnerOpenIO) LeaseCount() int {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	return len(driver.leases)
}

type ownerLease struct {
	id          uint64
	driver      *OwnerOpenIO
	ctx         context.Context
	cancel      context.CancelFunc
	timer       *time.Timer
	stopParent  func() bool
	mu          sync.Mutex
	closed      bool
	connection  io.Closer
	body        io.ReadCloser
	decoder     io.Closer
	traceBranch *responseBranch
	work        sync.WaitGroup
	closeOnce   sync.Once
}

func (lease *ownerLease) begin() bool {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed {
		return false
	}
	lease.work.Add(1)
	return true
}

func (lease *ownerLease) setConnection(connection io.Closer) bool {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed {
		_ = connection.Close()
		return false
	}
	lease.connection = connection
	return true
}

func (lease *ownerLease) closePhysical() {
	lease.mu.Lock()
	lease.closed = true
	connection := lease.connection
	lease.mu.Unlock()
	lease.cancel()
	if connection != nil {
		_ = connection.Close()
	}
}

func (lease *ownerLease) Read(buffer []byte) (int, error) {
	lease.mu.Lock()
	body := lease.body
	lease.mu.Unlock()
	if body == nil {
		return 0, io.EOF
	}
	count, err := body.Read(buffer)
	if err != nil {
		lease.closePhysical()
	}
	return count, err
}

func (lease *ownerLease) Close() error {
	lease.closeOnce.Do(func() {
		lease.mu.Lock()
		branch := lease.traceBranch
		lease.mu.Unlock()
		if branch != nil {
			_ = branch.Close()
		} else {
			_ = lease.closeAll()
		}
	})
	return nil
}

func (lease *ownerLease) closeAll() error {
	lease.closePhysical()
	lease.work.Wait()
	lease.mu.Lock()
	decoder := lease.decoder
	lease.mu.Unlock()
	if decoder != nil {
		_ = decoder.Close()
	}
	if lease.stopParent != nil {
		lease.stopParent()
	}
	lease.driver.mu.Lock()
	delete(lease.driver.leases, lease.id)
	lease.driver.mu.Unlock()
	return nil
}
