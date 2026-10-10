//go:build cch_native_ref_probe

package cooperative

// ProbeLifetimeDone exposes only the original immutable cancellation signal.
// This fixture-only accessor changes no Owner continuation or Promise identity.
func (owner *Owner) ProbeLifetimeDone() <-chan struct{} { return owner.life.Done() }

// ProbeMicrotaskReceipts returns the actual queued receipts by identity. It does
// not remove, invoke, wrap, replace or enqueue any original command.
func (owner *Owner) ProbeMicrotaskReceipts() []*Receipt {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	out := make([]*Receipt, len(owner.microtasks))
	for index, command := range owner.microtasks {
		out[index] = command.receipt
	}
	return out
}
