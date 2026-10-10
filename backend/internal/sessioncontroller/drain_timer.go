package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"sync"
	"time"
)

// scheduleDrainReference implements Source's referenced setTimeout, not a
// tracked Write/Record Promise. Admission occurs only for a real queued entry.
// The physical timer and its same-owner drain entry borrow the existing ledger.
func (pending *PendingWriter) scheduleDrainReference(scope *cooperative.Scope) (func(), error) {
	ref, err := pending.NativeCopyBinding().Acquire(scope)
	if err != nil {
		return nil, err
	}
	var lock sync.Mutex
	fired, cancelled := false, false
	stop := pending.writer.options.Schedule(100*time.Millisecond, func() {
		lock.Lock()
		if cancelled {
			lock.Unlock()
			return
		}
		fired = true
		lock.Unlock()
		pending.postDrainReference(ref)
	})
	return func() {
		lock.Lock()
		if cancelled {
			lock.Unlock()
			return
		}
		cancelled = true
		alreadyFired := fired
		lock.Unlock()
		stop()
		// A readiness already sent retains its reference through owner delivery.
		// Clearing an unfired timer releases exactly that timer's idempotent lease.
		if !alreadyFired {
			ref.DeliveryFinished()
		}
	}, nil
}
