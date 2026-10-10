package ownerasync

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"sync"
)

// NativeReferences is the one physical-job ledger borrowed by a Writer and its
// actual file-history copy producer. Promise obligations are never references.
// Its zero value retains the existing internal Writer counter contract.
type NativeReferences struct {
	mu      sync.Mutex
	count   int
	idle    chan struct{}
	binding *NativeBinding
}

// NativeBinding contains only its ledger and owner, never a callback. Bind is
// public: identity checking is not proof that an arbitrary supplied ledger came
// from a process's real Writer. The actual factory call chain owns that choice.
type NativeBinding struct {
	refs  *NativeReferences
	owner *cooperative.Owner
}

func (refs *NativeReferences) Bind(owner *cooperative.Owner) (*NativeBinding, error) {
	if refs == nil || owner == nil {
		return nil, cooperative.ErrRestricted
	}
	refs.mu.Lock()
	defer refs.mu.Unlock()
	if refs.binding != nil {
		if refs.binding.owner != owner {
			return nil, cooperative.ErrRestricted
		}
		return refs.binding, nil
	}
	refs.binding = &NativeBinding{refs: refs, owner: owner}
	return refs.binding, nil
}
func (binding *NativeBinding) Owns(owner *cooperative.Owner) bool {
	return binding != nil && binding.refs != nil && binding.owner == owner && owner != nil
}
func (binding *NativeBinding) Same(other *NativeBinding) bool {
	return binding != nil && binding == other && binding.Owns(binding.owner)
}
func (refs *NativeReferences) Begin() {
	refs.mu.Lock()
	defer refs.mu.Unlock()
	if refs.count == 0 {
		refs.idle = make(chan struct{})
	}
	refs.count++
}
func (refs *NativeReferences) End() {
	refs.mu.Lock()
	defer refs.mu.Unlock()
	refs.count--
	if refs.count == 0 {
		close(refs.idle)
	}
}
func (refs *NativeReferences) Snapshot() (int, <-chan struct{}) {
	refs.mu.Lock()
	defer refs.mu.Unlock()
	return refs.count, refs.idle
}

// Acquire happens on owner before dispatch. DeliveryFinished is administrative:
// it follows real owner completion and an admitted promise checkpoint, or closes
// the reference after rejected delivery when that owner no longer exists.
func (binding *NativeBinding) Acquire(scope *cooperative.Scope) (*NativeReference, error) {
	if binding == nil || !binding.Owns(binding.owner) || !scope.BelongsTo(binding.owner) {
		return nil, cooperative.ErrRestricted
	}
	var ref *NativeReference
	err := scope.Inline(cooperative.Read, func(*cooperative.Scope) error {
		binding.refs.Begin()
		ref = &NativeReference{binding: binding}
		return nil
	})
	return ref, err
}

type NativeReference struct {
	binding *NativeBinding
	once    sync.Once
}

func (ref *NativeReference) DeliveryFinished() {
	if ref != nil && ref.binding != nil {
		ref.once.Do(ref.binding.refs.End)
	}
}

// ReclaimRejectedReceipt observes only an already-admitted native completion's
// release receipt. The physical operation must have completed before this is
// called. A rejected queued release cannot run business, but its actual receipt
// still settles, allowing once-only administrative reclamation. Success keeps
// DeliveryFinished at the original release microtask's position. The returned
// channel is an administrative receipt, not a Source Promise or business wait.
func (ref *NativeReference) ReclaimRejectedReceipt(receipt *cooperative.Receipt) <-chan struct{} {
	done := make(chan struct{})
	if ref == nil || receipt == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		if receipt.Wait() != nil {
			ref.DeliveryFinished()
		}
	}()
	return done
}
