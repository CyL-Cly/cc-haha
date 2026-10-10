package provider

import (
	"errors"
	"io"
)

// A published TLS recordReader proves verification/handshake completed before
// the HTTP header read hit EOF. Handshake and other unknown TLS faults retain
// their existing guards. This error record is the actual Bun fetch observation.
func (reservation *NativeHTTP1Reservation) verifiedTLSHeaderClose(err error, endpoint string) (error, bool) {
	if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, false
	}
	reservation.mu.Lock()
	verified := reservation.operation != nil && reservation.operation.recordReader != nil
	reservation.mu.Unlock()
	if !verified {
		return nil, false
	}
	return &TLSConnectionFault{code: "ECONNRESET", path: endpoint, message: "The socket connection was closed unexpectedly. For more information, pass `verbose: true` in the second argument to fetch()", verification: err}, true
}
