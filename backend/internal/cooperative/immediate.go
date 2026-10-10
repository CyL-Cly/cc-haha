package cooperative

// ImmediateTicket is the VM-native resource used by pinned Bun's ImmediateObject
// (0d9b296af). It belongs to this Owner, not a worker, ordinary Read command,
// promise reaction, clock deadline, or a kernel-poll/check-phase certificate.
// This finite API ports referenced, zero-argument callbacks. JS ref/unref, values,
// arbitrary foreign-runtime scheduling and callback exceptions are not claimed.
type ImmediateTicket struct {
	owner    *Owner
	callback func(*Scope) error
	active   bool
}

// ArmImmediate enqueues a separate native immediate object. The current batch
// is a snapshot; objects created by its callbacks remain for a later batch.
func (scope *Scope) ArmImmediate(callback func(*Scope) error) (*ImmediateTicket, error) {
	if err := scope.check(); err != nil {
		return nil, err
	}
	if callback == nil {
		return nil, ErrRestricted
	}
	owner := scope.owner
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.closing {
		return nil, ErrClosed
	}
	ticket := &ImmediateTicket{owner: owner, callback: callback, active: true}
	owner.immediates = append(owner.immediates, ticket)
	select {
	case owner.wake <- struct{}{}:
	default:
	}
	return ticket, nil
}

// ClearImmediate retains the queued object until the native batch consumes it,
// as Bun cancel does, but releases the callback and prevents its invocation.
// The same caller's finally may clear its resource after cancellation.
func (scope *Scope) ClearImmediate(ticket *ImmediateTicket) error {
	if scope == nil || !scope.valid || scope.owner == nil || ticket == nil || ticket.owner != scope.owner {
		return ErrRestricted
	}
	ticket.active = false
	ticket.callback = nil
	return nil
}

// AwaitImmediate resolves on this resource's callback and resumes through its
// own promise reaction. It does not join pending reads/watchers, use a timer,
// sleep, or infer eligibility from an observed Source phase.
func (scope *Scope) AwaitImmediate() error {
	if err := scope.check(); err != nil {
		return err
	}
	done := make(chan struct{})
	ticket, err := scope.ArmImmediate(func(callbackScope *Scope) error {
		_, err := callbackScope.owner.Microtask(Read, func(*Scope) error { close(done); return nil })
		return err
	})
	if err != nil {
		return err
	}
	defer scope.ClearImmediate(ticket)
	return scope.AwaitPromise(done)
}

// beginImmediateBatch is pinned Bun autoTick's native admission: tick() has
// consumed currently ready tasks/microtasks; autoTick snapshots immediate_tasks
// before polling. New objects never extend an already admitted batch. A finished
// batch gives existing due-timer policy an opportunity before another batch.
// It deliberately does not wait for all pending native work to become ready.
func (scope *Scope) beginImmediateBatch() bool {
	owner := scope.owner
	if owner.immediateBatchEnded {
		owner.immediateBatchEnded = false
		if scope.fireDueTimers() {
			return true
		}
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.closing || len(owner.microtasks) != 0 || len(owner.queue) != 0 || len(owner.immediateBatch) != 0 || len(owner.immediates) == 0 {
		return false
	}
	owner.immediateBatch, owner.immediates = owner.immediates, nil
	return true
}

// takeImmediateLocked is called only with Owner.mu held. Microtasks remain
// first; the remainder of an admitted batch precedes later ordinary I/O.
func (owner *Owner) takeImmediateLocked() (command, bool) {
	if len(owner.immediateBatch) == 0 {
		return command{}, false
	}
	ticket := owner.immediateBatch[0]
	owner.immediateBatch[0] = nil
	owner.immediateBatch = owner.immediateBatch[1:]
	if len(owner.immediateBatch) == 0 {
		owner.immediateBatchEnded = true
	}
	next := command{class: Read, independent: true, receipt: &Receipt{done: make(chan struct{})}, immediate: ticket}
	next.run = func(scope *Scope) error {
		callback := ticket.callback
		ticket.active = false
		ticket.callback = nil
		if callback == nil {
			return nil
		}
		return callback(scope)
	}
	return next, true
}

func (owner *Owner) closeImmediates() {
	for _, list := range [][]*ImmediateTicket{owner.immediateBatch, owner.immediates} {
		for _, ticket := range list {
			ticket.active = false
			ticket.callback = nil
		}
	}
	owner.immediateBatch, owner.immediates = nil, nil
	owner.immediateBatchEnded = false
}
