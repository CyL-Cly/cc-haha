package sessioncontroller

import (
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// PendingWriter borrows the canonical pre-activation instance exactly once for
// the SDK Host. Reuse is not authorization for a second Host/serial producer.
func (writer *Writer) PendingWriter(owner *cooperative.Owner) (*PendingWriter, error) {
	if writer == nil || owner == nil {
		return nil, cooperative.ErrRestricted
	}
	writer.mu.Lock()
	p := writer.async
	writer.mu.Unlock()
	if p == nil {
		var e error
		p, e = NewPendingWriter(writer, owner)
		if e != nil {
			return nil, e
		}
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if p.owner != owner {
		return nil, cooperative.ErrRestricted
	}
	if p.hostClaimed {
		return nil, errors.New("writer already has an SDK Host borrower")
	}
	p.hostClaimed = true
	return p, nil
}
