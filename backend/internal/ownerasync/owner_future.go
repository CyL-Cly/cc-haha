package ownerasync

import (
	"context"
	"errors"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// OwnerFuture represents a finite native Promise value. All resolution and
// reaction registration belongs to one Owner. T must not expose mutable business
// state to a physical worker. Administrative Close only rejects an obligation.
type OwnerFuture[T any] struct {
	owner          *cooperative.Owner
	identity       *PromiseIdentity
	handled        bool
	administrative bool
	ignored        *IgnoredRejections
	ignoredHolder  *PromiseIdentity
	ignoredSite    string
	mu             sync.Mutex
	done           chan struct{}
	settled        bool
	claimed        bool
	value          T
	err            error
	reactions      []func(T, error)
}

func NewOwnerFuture[T any](owner *cooperative.Owner) (*OwnerFuture[T], error) {
	if owner == nil {
		return nil, cooperative.ErrRestricted
	}
	future := &OwnerFuture[T]{owner: owner, identity: &PromiseIdentity{marker: 1}, done: make(chan struct{})}
	go func() {
		select {
		case <-future.done:
		case <-owner.Done():
			var zero T
			future.finishAdministrative(zero, cooperative.ErrClosed)
		}
	}()
	return future, nil
}

func (future *OwnerFuture[T]) Done() <-chan struct{} { return future.done }

func (future *OwnerFuture[T]) check(scope *cooperative.Scope) error {
	if future == nil || !scope.BelongsTo(future.owner) {
		return cooperative.ErrRestricted
	}
	return scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil })
}

// finish is also the administrative rejection seam. It publishes no business
// callback on the closing worker; registered reactions post only onto Owner.
func (future *OwnerFuture[T]) finish(value T, err error) { future.finishValue(value, err, false) }
func (future *OwnerFuture[T]) finishAdministrative(value T, err error) {
	future.finishValue(value, err, true)
}
func (future *OwnerFuture[T]) finishValue(value T, err error, administrative bool) {
	future.mu.Lock()
	defer future.mu.Unlock()
	if future.settled {
		return
	}
	future.settled, future.claimed = true, true
	future.value, future.err, future.administrative = value, err, administrative
	if future.ignored != nil && err != nil && !administrative {
		future.ignored.rejected(future.identity, future.ignoredHolder, future.ignoredSite, err, future.handled)
	}
	for _, reaction := range future.reactions {
		reaction(value, err)
	}
	future.reactions = nil
	close(future.done)
}

func (future *OwnerFuture[T]) claim(scope *cooperative.Scope) (bool, error) {
	if err := future.check(scope); err != nil {
		return false, err
	}
	future.mu.Lock()
	defer future.mu.Unlock()
	if future.claimed {
		return false, nil
	}
	future.claimed = true
	return true, nil
}

func (future *OwnerFuture[T]) Resolve(scope *cooperative.Scope, value T) error {
	claimed, err := future.claim(scope)
	if err != nil || !claimed {
		return err
	}
	future.finish(value, nil)
	return nil
}

func (future *OwnerFuture[T]) Reject(scope *cooperative.Scope, failure error) error {
	if failure == nil {
		return errors.New("owner future rejection requires an error")
	}
	claimed, err := future.claim(scope)
	if err != nil || !claimed {
		return err
	}
	var zero T
	future.finish(zero, failure)
	return nil
}

// ResolveFrom preserves Promise resolution's adoption job followed by the
// adopted Promise's forwarding reaction. It does not pump an arbitrary number
// of fulfilled awaits and cannot adopt a different Owner's future.
func (future *OwnerFuture[T]) ResolveFrom(scope *cooperative.Scope, source *OwnerFuture[T]) error {
	if err := future.check(scope); err != nil {
		return err
	}
	if source == nil || source.owner != future.owner || source == future {
		return cooperative.ErrRestricted
	}
	claimed, err := future.claim(scope)
	if err != nil || !claimed {
		return err
	}
	_, err = future.owner.Microtask(cooperative.Read, func(next *cooperative.Scope) error {
		_, err := source.Then(next, func(_ *cooperative.Scope, value T, failure error) error {
			future.finish(value, failure)
			return nil
		})
		return err
	})
	if err != nil {
		var zero T
		future.finish(zero, err)
	}
	return err
}

// Then returns the dependent completion Promise. A callback is always an Owner
// microtask, including an already-settled parent. Its Scope cannot run a Frame.
func (future *OwnerFuture[T]) Then(scope *cooperative.Scope, run func(*cooperative.Scope, T, error) error) (*OwnerFuture[struct{}], error) {
	if err := future.check(scope); err != nil {
		return nil, err
	}
	if run == nil {
		return nil, cooperative.ErrRestricted
	}
	child, _ := NewOwnerFuture[struct{}](future.owner)
	post := func(value T, failure error) {
		_, err := future.owner.Microtask(cooperative.Read, func(next *cooperative.Scope) error {
			if err := run(next, value, failure); err != nil {
				return child.Reject(next, err)
			}
			return child.Resolve(next, struct{}{})
		})
		if err != nil {
			child.finish(struct{}{}, err)
		}
	}
	future.mu.Lock()
	defer future.mu.Unlock()
	future.handled = true
	if future.ignored != nil {
		future.ignored.handled(future.identity)
	}
	if future.settled {
		post(future.value, future.err)
	} else {
		future.reactions = append(future.reactions, post)
	}
	return child, nil
}

// Await is a Promise await, even when already settled. It stops at its own FIFO
// reaction rather than consuming reactions queued after the caller continuation.
func (future *OwnerFuture[T]) Await(scope *cooperative.Scope) (T, error) {
	value, failure, _ := future.AwaitClassified(scope)
	return value, failure
}

// AwaitClassified retains the exact Await reaction while distinguishing the
// caller's administrative wait failure from the producer's original rejection.
// It adds no Promise/job and does not reinterpret model AbortError.
func (future *OwnerFuture[T]) AwaitClassified(scope *cooperative.Scope) (T, error, bool) {
	var value T
	var failure error
	done := make(chan struct{})
	_, err := future.Then(scope, func(_ *cooperative.Scope, result T, rejected error) error {
		value, failure = result, rejected
		close(done)
		return nil
	})
	if err != nil {
		return value, err, true
	}
	if err := scope.AwaitPromise(done); err != nil {
		return value, err, true
	}
	return value, failure, false
}

// RejectAdministrative settles only an immutable rejected obligation. It may
// override pending adoption, never resolves T or executes business off-owner.
func (future *OwnerFuture[T]) RejectAdministrative(failure error) error {
	if future == nil || failure == nil {
		return cooperative.ErrRestricted
	}
	var zero T
	future.finishAdministrative(zero, failure)
	return nil
}

// Wait is administrative observation only. Callers may observe immutable
// receipts off-owner, but must not mutate a business value read through T.
func (future *OwnerFuture[T]) Wait(ctx context.Context) (T, error) {
	var zero T
	if ctx == nil {
		return zero, cooperative.ErrRestricted
	}
	select {
	case <-future.done:
		future.mu.Lock()
		defer future.mu.Unlock()
		return future.value, future.err
	case <-ctx.Done():
		return zero, context.Cause(ctx)
	}
}
