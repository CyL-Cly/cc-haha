package cooperative

import "context"

// Microtask admits a promise reaction with owner lifetime. It is distinct from
// physical I/O readiness and from Input/timer commands, and cannot start an ASK
// or a provider frame. No timer, sleep or Source-observed phase drives it.
func (owner *Owner) Microtask(class Class, run func(*Scope) error) (*Receipt, error) {
	if !allowed(class) || run == nil {
		return nil, ErrRestricted
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.closing {
		return nil, ErrClosed
	}
	receipt := &Receipt{done: make(chan struct{})}
	owner.microtasks = append(owner.microtasks, command{class: class, run: run, receipt: receipt, independent: true})
	select {
	case owner.wake <- struct{}{}:
	default:
	}
	return receipt, nil
}
func (owner *Owner) takeMicrotask() (command, bool) {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if len(owner.microtasks) == 0 {
		return command{}, false
	}
	next := owner.microtasks[0]
	owner.microtasks[0] = command{}
	owner.microtasks = owner.microtasks[1:]
	return next, true
}
func (owner *Owner) runCallback(next command) error {
	if next.sideContinuation != nil {
		return next.sideContinuation.advance()
	}
	if owner.life.Err() != nil {
		return ErrClosed
	}
	scope := &Scope{owner: owner, ctx: owner.life, valid: true}
	defer func() { scope.valid = false }()
	return scope.Inline(next.class, next.run)
}

// AwaitPromise waits only for an admitted immutable promise settlement. A
// fulfilled reaction resumes before the next I/O/timer command. Cancellation
// stops this observer, leaving independent persistence and its finally alive.
// This finite primitive does not claim all Bun native-event-loop priorities.
func (scope *Scope) AwaitPromise(done <-chan struct{}) error {
	if scope.sideContinuation != nil {
		if err := scope.check(); err != nil {
			return err
		}
		return scope.pauseSideQuestion(done)
	}
	if err := scope.check(); err != nil {
		return err
	}
	if done == nil {
		return ErrRestricted
	}
	for {
		// done is closed by this await's FIFO reaction. Reactions appended
		// behind it belong after the suspended continuation resumes.
		select {
		case <-done:
			return nil
		default:
		}
		if next, ok := scope.owner.takeMicrotask(); ok {
			finish(next, scope.owner.runCallback(next))
			continue
		}
		select {
		case <-done:
			return nil
		default:
		}
		if scope.ctx.Err() != nil {
			return context.Cause(scope.ctx)
		}
		if next, ok := scope.owner.takeCallback(); ok {
			finish(next, scope.runQueued(next))
			continue
		}
		// A pending promise does not freeze the owner's timers. Own fulfilled
		// reactions and already queued commands retain their earlier cutpoints.
		if scope.beginImmediateBatch() || scope.fireDueTimers() {
			continue
		}
		select {
		case <-done:
			return nil
		case <-scope.owner.wake:
		case <-scope.ctx.Done():
			return context.Cause(scope.ctx)
		}
	}
}

// AwaitFulfilled ports an await of an already-resolved async return. It keeps
// the actual reaction boundary without an artificial asynchronous I/O worker.
func (scope *Scope) AwaitFulfilled() error {
	if err := scope.check(); err != nil {
		return err
	}
	done := make(chan struct{})
	if _, err := scope.owner.Microtask(Read, func(*Scope) error { close(done); return nil }); err != nil {
		return err
	}
	return scope.AwaitPromise(done)
}

// AwaitFulfilledCatch preserves await alreadyFulfilled.catch(handler): the
// forwarding reaction settles the returned Promise before its await reaction
// is admitted. No handler runs on this finite successful/disabled branch.
func (scope *Scope) AwaitFulfilledCatch() error {
	if err := scope.check(); err != nil {
		return err
	}
	done := make(chan struct{})
	owner := scope.owner
	if _, err := owner.Microtask(Read, func(*Scope) error {
		_, err := owner.Microtask(Read, func(*Scope) error { close(done); return nil })
		return err
	}); err != nil {
		return err
	}
	return scope.AwaitPromise(done)
}

// RegisterPromiseReaction executes only the registration on owner. The
// supplied closure may register a future callback, never run a provider frame.
func (scope *Scope) RegisterPromiseReaction(register func(*Owner) error) error {
	if err := scope.check(); err != nil {
		return err
	}
	if register == nil {
		return ErrRestricted
	}
	return register(scope.owner)
}
