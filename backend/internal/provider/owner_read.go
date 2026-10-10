package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
)

// IsOwnerWaitFailure is an administrative scheduler failure, never a native
// reader/SDK error. The model adapter must not turn it into caller-abort output.
func IsOwnerWaitFailure(err error) bool {
	var failure *ownerWaitFailure
	return errors.As(err, &failure)
}

// ownerBodyReader remains on the original owner stack. Only one demanded native
// Read runs in the worker; the decoder and all event/error policy remain owner.
type ownerBodyReader struct {
	driver *OwnerOpenIO
	lease  *ownerLease
}

func (driver *OwnerOpenIO) streamBody(body io.ReadCloser) (io.ReadCloser, error) {
	lease, ok := body.(*ownerLease)
	if !ok || lease.driver != driver {
		return nil, fmt.Errorf("%w: owner stream-body lease", ErrHTTPExchangeProfile)
	}
	return &ownerBodyReader{driver, lease}, nil
}
func (body *ownerBodyReader) Read(out []byte) (int, error) {
	return readDemandedOwnerBody(body.driver.await, body.lease, out)
}

// ownerReadLease exposes only private physical resource operations. In the
// native path it contains no owner bridge, Scope or mutable message state.
type ownerReadLease interface {
	io.ReadCloser
	beginRead() bool
	endRead()
	closePhysical()
}

func (lease *ownerLease) beginRead() bool { return lease.begin() }
func (lease *ownerLease) endRead()        { lease.work.Done() }

func readDemandedOwnerBody(await OwnerAwait, lease ownerReadLease, out []byte) (int, error) {
	capacity := len(out)
	raw, err := await(OwnerBodyRead, func(wait context.Context) ([]byte, error) {
		if !lease.beginRead() {
			// Closing a response lease does not cancel the original caller.
			// Only the caller/SDK context supplies abort identity to its consumer.
			return nil, &ownerNativeFault{"body", net.ErrClosed}
		}
		defer lease.endRead()
		stop := context.AfterFunc(wait, lease.closePhysical)
		defer stop()
		scratch := make([]byte, capacity)
		count, err := lease.Read(scratch)
		if err != nil {
			return scratch[:count], &ownerNativeFault{"body", err}
		}
		return scratch[:count], nil
	})
	if len(raw) > capacity {
		_ = lease.Close()
		return 0, fmt.Errorf("%w: owner Read returned beyond its requested capacity", ErrHTTPExchangeProfile)
	}
	if err != nil {
		var native *ownerNativeFault
		if errors.As(err, &native) {
			return copy(out, raw), native.cause
		} // Exact io.EOF matters to the pinned decoder.
		_ = lease.Close()
		return 0, &ownerWaitFailure{err}
	}
	return copy(out, raw), nil
}
func (body *ownerBodyReader) Close() error { return body.lease.Close() }
