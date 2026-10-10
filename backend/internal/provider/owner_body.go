package provider

import (
	"fmt"
	"io"
	"sync"
)

// OwnerBodyIO binds demanded body reads to the fresh native-header producer.
// It creates no stream, decoder, retry, history projection or model consumer.
type OwnerBodyIO struct {
	native *OwnerNativeIO
	await  OwnerAwait
}

func NewOwnerBodyIO(native *OwnerNativeIO, await OwnerAwait) (*OwnerBodyIO, error) {
	if native == nil || await == nil {
		return nil, fmt.Errorf("%w: explicit native body owner required", ErrHTTPExchangeProfile)
	}
	if err := native.CheckOwner(); err != nil {
		return nil, err
	}
	return &OwnerBodyIO{native: native, await: await}, nil
}
func (driver *OwnerBodyIO) checked(body io.ReadCloser) (*nativeOwnerBody, error) {
	if driver == nil || driver.native == nil {
		return nil, fmt.Errorf("%w: native body driver", ErrHTTPExchangeProfile)
	}
	if err := driver.native.CheckOwner(); err != nil {
		return nil, err
	}
	lease, ok := body.(*nativeOwnerBody)
	if !ok || lease.driver != driver.native || lease.physical == nil {
		return nil, fmt.Errorf("%w: original native body lease identity", ErrHTTPExchangeProfile)
	}
	return lease, nil
}
func (driver *OwnerBodyIO) streamBody(body io.ReadCloser) (io.ReadCloser, error) {
	lease, err := driver.checked(body)
	if err != nil {
		return nil, err
	}
	return &nativeBodyReader{driver: driver, lease: lease}, nil
}

// WrapFetchBody preserves the exact native lease and parks each demanded read
// on its current owner. Credential consumers may reject/close an HTTP status
// before reading its body, just as their original Node transport does.
func (driver *OwnerBodyIO) WrapFetchBody(body io.ReadCloser) (io.ReadCloser, error) {
	return driver.streamBody(body)
}

func (driver *OwnerBodyIO) errorBody(body io.ReadCloser) ([]byte, error) {
	lease, err := driver.checked(body)
	if err != nil {
		return nil, err
	}
	raw, failure := readOwnerErrorBody(driver.await, lease.physical)
	return raw, nativeBodyReadError(failure)
}

// ReadFetchBody consumes only a lease returned by this exact native driver.
// Physical workers return bytes; the owning caller parses the response.
func (driver *OwnerBodyIO) ReadFetchBody(body io.ReadCloser) ([]byte, error) {
	return driver.errorBody(body)
}

type nativeBodyReader struct {
	driver *OwnerBodyIO
	lease  *nativeOwnerBody
}

func (body *nativeBodyReader) Read(out []byte) (int, error) {
	if _, err := body.driver.checked(body.lease); err != nil {
		_ = body.lease.Close()
		return 0, &ownerWaitFailure{err}
	}
	n, failure := readDemandedOwnerBody(body.driver.await, body.lease.physical, out)
	return n, nativeBodyReadError(failure)
}
func (body *nativeBodyReader) Close() error { return body.lease.Close() }

// This object is the only native body lease passed to an I/O worker. It has no
// Scope/native driver or message state; closure/join only owns its descriptor.
type nativeBodyReadLease struct {
	body     io.ReadCloser
	decoder  io.Closer
	resource *NativeHTTP1Reservation
	mu       sync.Mutex
	closed   bool
	readers  sync.WaitGroup
}

func (lease *nativeBodyReadLease) beginRead() bool {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed {
		return false
	}
	lease.readers.Add(1)
	return true
}
func (lease *nativeBodyReadLease) endRead()                     { lease.readers.Done() }
func (lease *nativeBodyReadLease) Read(out []byte) (int, error) { return lease.body.Read(out) }
func (lease *nativeBodyReadLease) closePhysical() {
	lease.mu.Lock()
	lease.closed = true
	lease.mu.Unlock()
	_ = lease.resource.Close()
}
func (lease *nativeBodyReadLease) Close() error {
	lease.mu.Lock()
	lease.closed = true
	lease.mu.Unlock()
	err := lease.resource.Close()
	lease.readers.Wait()
	if lease.decoder != nil {
		_ = lease.decoder.Close()
	}
	return err
}

var _ ownerReadLease = (*nativeBodyReadLease)(nil)
