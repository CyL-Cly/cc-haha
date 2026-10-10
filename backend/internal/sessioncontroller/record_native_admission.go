package sessioncontroller

import "sync"

// recordNativeAdmission only fences administrative Record dispatch. General
// prepare/drain/descriptor cleanup and shared file-history refs retain their
// own already-admitted lifetime. This is not a Source Project flush barrier.
type recordNativeAdmission struct {
	mu     sync.Mutex
	closed bool
	done   chan struct{}
}

// All callers are original owner continuations. The lock ties the stop mark to
// worker Add and native-ref admission so off-owner Close cannot observe an
// empty worker set and then acquire a newly dispatched Record worker.
func (pending *PendingWriter) admitRecordWorker(native bool) (<-chan struct{}, error) {
	gate := &pending.recordAdmission
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.closed || pending.closing {
		return nil, ErrPendingClosed
	}
	if gate.done == nil {
		gate.done = make(chan struct{})
	}
	if native {
		pending.runtimeNative.begin()
	}
	pending.workers.Add(1)
	return gate.done, nil
}

func (pending *PendingWriter) recordAdmissionError() error {
	gate := &pending.recordAdmission
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.closed || pending.closing {
		return ErrPendingClosed
	}
	return nil
}

// stopRecordAdmission runs on Owner at the actual Close admission point, after
// the original drain has begun. It wakes only pure legacy branch observers;
// already admitted physical operations are neither canceled nor replaced.
func (pending *PendingWriter) stopRecordAdmission() {
	gate := &pending.recordAdmission
	gate.mu.Lock()
	defer gate.mu.Unlock()
	pending.closing = true
	if !gate.closed {
		gate.closed = true
		if gate.done == nil {
			gate.done = make(chan struct{})
		}
		close(gate.done)
	}
}
