package ownerasync

import (
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

type IteratorResult[T any] struct {
	Done    bool `json:"done"`
	Defined bool `json:"defined"`
	Value   T    `json:"value,omitempty"`
}

// MarshalJSON is the finite observation descriptor, not a model request mapper.
// Defined zero/empty/null values must survive; an undefined done value is omitted.
func (result IteratorResult[T]) MarshalJSON() ([]byte, error) {
	if result.Defined {
		return json.Marshal(struct {
			Done    bool `json:"done"`
			Defined bool `json:"defined"`
			Value   T    `json:"value"`
		}{result.Done, result.Defined, result.Value})
	}
	return json.Marshal(struct {
		Done    bool `json:"done"`
		Defined bool `json:"defined"`
	}{result.Done, false})
}

type IteratorRequest[T any] struct {
	Returning bool
	Throwing  bool
	Failure   error
	Defined   bool
	Value     T
}

// OwnerAsyncIterator is the same-owner asynchronous demand boundary. A real
// AsyncFromSync adapter may implement it without pretending to be an async
// generator. Existing OwnerIterator callers keep their original methods.
type OwnerAsyncIterator[T any] interface {
	AsyncIteratorOwner() *cooperative.Owner
	Next(*cooperative.Scope) (*OwnerFuture[IteratorResult[T]], error)
	Return(*cooperative.Scope, T) (*OwnerFuture[IteratorResult[T]], error)
	ReturnUndefined(*cooperative.Scope) (*OwnerFuture[IteratorResult[T]], error)
}

func (iterator *OwnerIterator[T]) AsyncIteratorOwner() *cooperative.Owner {
	if iterator == nil {
		return nil
	}
	return iterator.owner
}

type GeneratorContinuation[T any] func(*cooperative.Scope, *OwnerIterator[T], IteratorRequest[T]) error

type iteratorRequest[T any] struct {
	request IteratorRequest[T]
	future  *OwnerFuture[IteratorResult[T]]
}

// OwnerIterator ports the finite async-generator demand/yield/await machinery.
// It never invokes provider policy from a worker or creates a second Owner.
type OwnerIterator[T any] struct {
	owner      *cooperative.Owner
	start      GeneratorContinuation[T]
	resume     GeneratorContinuation[T]
	queue      []iteratorRequest[T]
	active     *iteratorRequest[T]
	started    bool
	executing  bool
	awaiting   bool
	held       bool
	completed  bool
	generation uint64
}

func NewOwnerIterator[T any](owner *cooperative.Owner, start GeneratorContinuation[T]) (*OwnerIterator[T], error) {
	if owner == nil || start == nil {
		return nil, cooperative.ErrRestricted
	}
	return &OwnerIterator[T]{owner: owner, start: start}, nil
}

func (iterator *OwnerIterator[T]) check(scope *cooperative.Scope) error {
	if iterator == nil || !scope.BelongsTo(iterator.owner) {
		return cooperative.ErrRestricted
	}
	return scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil })
}

func (iterator *OwnerIterator[T]) Next(scope *cooperative.Scope) (*OwnerFuture[IteratorResult[T]], error) {
	return iterator.request(scope, IteratorRequest[T]{})
}

func (iterator *OwnerIterator[T]) Return(scope *cooperative.Scope, value T) (*OwnerFuture[IteratorResult[T]], error) {
	return iterator.request(scope, IteratorRequest[T]{Returning: true, Defined: true, Value: value})
}

func (iterator *OwnerIterator[T]) ReturnUndefined(scope *cooperative.Scope) (*OwnerFuture[IteratorResult[T]], error) {
	return iterator.request(scope, IteratorRequest[T]{Returning: true})
}

// Throw resumes the original suspended catch/finally with the same error.
// It queues behind an in-flight Next; it is not a transport cancellation.
func (iterator *OwnerIterator[T]) Throw(scope *cooperative.Scope, failure error) (*OwnerFuture[IteratorResult[T]], error) {
	if failure == nil {
		return nil, cooperative.ErrRestricted
	}
	return iterator.request(scope, IteratorRequest[T]{Throwing: true, Failure: failure})
}

func (iterator *OwnerIterator[T]) request(scope *cooperative.Scope, request IteratorRequest[T]) (*OwnerFuture[IteratorResult[T]], error) {
	if err := iterator.check(scope); err != nil {
		return nil, err
	}
	future, _ := NewOwnerFuture[IteratorResult[T]](iterator.owner)
	iterator.queue = append(iterator.queue, iteratorRequest[T]{request, future})
	if err := iterator.drain(scope); err != nil {
		return future, err
	}
	return future, nil
}

// drain runs the suspended generator synchronously in Next's call until its
// actual await/yield. Prequeued requests resume at yield fulfillment before the
// previous consumer reaction, matching the finite Bun original programs.
func (iterator *OwnerIterator[T]) drain(scope *cooperative.Scope) error {
	if iterator.active != nil || len(iterator.queue) == 0 {
		return nil
	}
	item := iterator.queue[0]
	iterator.queue = iterator.queue[1:]
	iterator.active = &item
	if iterator.completed || (!iterator.started && (item.request.Returning || item.request.Throwing)) {
		if item.request.Throwing {
			return iterator.Fail(scope, item.request.Failure)
		}
		iterator.completed = true
		if item.request.Returning {
			return iterator.awaitReturn(scope, nil)
		}
		iterator.active = nil
		if err := item.future.Resolve(scope, IteratorResult[T]{Done: true}); err != nil {
			return err
		}
		return iterator.drain(scope)
	}
	continuation := iterator.resume
	if !iterator.started {
		iterator.started = true
		continuation = iterator.start
	}
	iterator.resume = nil
	iterator.executing = true
	if continuation == nil {
		return iterator.Fail(scope, errors.New("generator has no suspended continuation"))
	}
	if item.request.Returning {
		return iterator.awaitReturn(scope, continuation)
	}
	return iterator.runContinuation(scope, continuation)
}

func (iterator *OwnerIterator[T]) runContinuation(scope *cooperative.Scope, continuation GeneratorContinuation[T]) error {
	item := iterator.active
	if err := continuation(scope, iterator, item.request); err != nil {
		return iterator.Fail(scope, err)
	}
	if iterator.executing && !iterator.awaiting && !iterator.held {
		return iterator.Fail(scope, errors.New("generator continuation did not yield, await, or complete"))
	}
	return nil
}

// The abrupt Return argument is awaited before resuming a suspended generator.
// Normal Next is synchronous until the body reaches an actual await/yield.
func (iterator *OwnerIterator[T]) awaitReturn(scope *cooperative.Scope, continuation GeneratorContinuation[T]) error {
	iterator.awaiting = true
	generation := iterator.generation
	future, _ := NewOwnerFuture[T](iterator.owner)
	_ = future.Resolve(scope, iterator.active.request.Value)
	_, err := future.Then(scope, func(next *cooperative.Scope, _ T, _ error) error {
		if iterator.generation != generation {
			return nil
		}
		iterator.awaiting = false
		if continuation == nil {
			return iterator.CompleteReturn(next)
		}
		return iterator.runContinuation(next, continuation)
	})
	return err
}

func (iterator *OwnerIterator[T]) canSuspend(scope *cooperative.Scope) error {
	if err := iterator.check(scope); err != nil {
		return err
	}
	if iterator.active == nil || !iterator.executing || iterator.awaiting {
		return cooperative.ErrRestricted
	}
	return nil
}

// HoldProducer retains this demand while the admitted original producer stack
// is in flight. The start/resume callback may publish only an immutable receipt;
// actual provider Frame/Next remains on that stack. This is not a synthetic
// value await or a permission to run provider policy inside a reaction callback.
func (iterator *OwnerIterator[T]) HoldProducer(scope *cooperative.Scope) error {
	if err := iterator.canSuspend(scope); err != nil {
		return err
	}
	if iterator.held {
		return cooperative.ErrRestricted
	}
	iterator.held = true
	return nil
}

func (iterator *OwnerIterator[T]) Yield(scope *cooperative.Scope, value T, resume GeneratorContinuation[T]) error {
	if err := iterator.canSuspend(scope); err != nil {
		return err
	}
	future, _ := NewOwnerFuture[T](iterator.owner)
	_ = future.Resolve(scope, value)
	return iterator.YieldFuture(scope, future, resume, nil)
}

// YieldFuture is AsyncGeneratorYield's value await. Rejection is delivered to
// the actual generator's catch/finally continuation, never silently normalized.
func (iterator *OwnerIterator[T]) YieldFuture(scope *cooperative.Scope, value *OwnerFuture[T], resume GeneratorContinuation[T], rejected func(*cooperative.Scope, error) error) error {
	if err := iterator.canSuspend(scope); err != nil {
		return err
	}
	if value == nil || value.owner != iterator.owner || resume == nil {
		return cooperative.ErrRestricted
	}
	iterator.awaiting = true
	iterator.held = false
	generation := iterator.generation
	_, err := value.Then(scope, func(next *cooperative.Scope, yielded T, failure error) error {
		if iterator.generation != generation {
			return nil
		}
		iterator.awaiting = false
		if failure != nil {
			if rejected != nil {
				if err := rejected(next, failure); err != nil {
					return iterator.Fail(next, err)
				}
				return nil
			}
			return iterator.Fail(next, failure)
		}
		current := iterator.active
		iterator.active = nil
		iterator.executing = false
		iterator.resume = resume
		if err := current.future.Resolve(next, IteratorResult[T]{Value: yielded, Defined: true}); err != nil {
			return err
		}
		return iterator.drain(next)
	})
	return err
}

func (iterator *OwnerIterator[T]) Complete(scope *cooperative.Scope) error {
	return iterator.finishCompletion(scope, IteratorResult[T]{Done: true})
}

// CompleteReturn propagates the already-awaited abrupt Return through an
// ordinary finally. CompleteValue separately represents an explicit return
// expression, whose value still requires its own await.
func (iterator *OwnerIterator[T]) CompleteReturn(scope *cooperative.Scope) error {
	if err := iterator.check(scope); err != nil {
		return err
	}
	if iterator.active == nil || !iterator.active.request.Returning {
		return cooperative.ErrRestricted
	}
	request := iterator.active.request
	return iterator.finishCompletion(scope, IteratorResult[T]{Done: true, Defined: request.Defined, Value: request.Value})
}

func (iterator *OwnerIterator[T]) CompleteValue(scope *cooperative.Scope, value T) error {
	return iterator.CompleteDefined(scope, value, true)
}

func (iterator *OwnerIterator[T]) CompleteDefined(scope *cooperative.Scope, value T, defined bool) error {
	if err := iterator.check(scope); err != nil {
		return err
	}
	if iterator.active == nil || iterator.awaiting {
		return cooperative.ErrRestricted
	}
	iterator.awaiting = true
	iterator.held = false
	future, _ := NewOwnerFuture[T](iterator.owner)
	_ = future.Resolve(scope, value)
	generation := iterator.generation
	_, err := future.Then(scope, func(next *cooperative.Scope, result T, _ error) error {
		if iterator.generation != generation {
			return nil
		}
		// This newly created literal-value future is fulfilled before registration.
		// Close rejects administratively without running this continuation.
		iterator.awaiting = false
		return iterator.finishCompletion(next, IteratorResult[T]{Done: true, Defined: defined, Value: result})
	})
	return err
}

func (iterator *OwnerIterator[T]) finishCompletion(scope *cooperative.Scope, result IteratorResult[T]) error {
	if err := iterator.check(scope); err != nil {
		return err
	}
	if iterator.active == nil || iterator.awaiting {
		return cooperative.ErrRestricted
	}
	iterator.executing, iterator.completed, iterator.held = false, true, false
	current := iterator.active
	iterator.active = nil
	if err := current.future.Resolve(scope, result); err != nil {
		return err
	}
	return iterator.drain(scope)
}

func (iterator *OwnerIterator[T]) Fail(scope *cooperative.Scope, failure error) error {
	if err := iterator.check(scope); err != nil {
		return err
	}
	if failure == nil || iterator.active == nil {
		return cooperative.ErrRestricted
	}
	iterator.completed, iterator.executing, iterator.awaiting = true, false, false
	iterator.held = false
	current := iterator.active
	iterator.active = nil
	if err := current.future.Reject(scope, failure); err != nil {
		return err
	}
	return iterator.drain(scope)
}

// AwaitGenerator keeps an explicit original await before its generator's next
// state. The resumed closure is owner-only, with no Frame privilege.
func AwaitGenerator[T, U any](scope *cooperative.Scope, iterator *OwnerIterator[T], awaited *OwnerFuture[U], run func(*cooperative.Scope, U) error, rejected func(*cooperative.Scope, error) error) error {
	if err := iterator.canSuspend(scope); err != nil {
		return err
	}
	if awaited == nil || awaited.owner != iterator.owner || run == nil {
		return cooperative.ErrRestricted
	}
	iterator.awaiting = true
	iterator.held = false
	generation := iterator.generation
	_, err := awaited.Then(scope, func(next *cooperative.Scope, value U, failure error) error {
		if iterator.generation != generation {
			return nil
		}
		iterator.awaiting = false
		if failure != nil {
			if rejected != nil {
				if err := rejected(next, failure); err != nil {
					return iterator.Fail(next, err)
				}
				return nil
			}
			return iterator.Fail(next, failure)
		}
		if err := run(next, value); err != nil {
			return iterator.Fail(next, err)
		}
		return nil
	})
	return err
}

// Delegate forwards the actual inner async iterator request and its Promise,
// then performs the outer yield's value await. Done and errors return to the
// original generator's post-delegation/catch/finally closures.
func (iterator *OwnerIterator[T]) Delegate(scope *cooperative.Scope, inner OwnerAsyncIterator[T], request IteratorRequest[T], done func(*cooperative.Scope, IteratorResult[T], IteratorRequest[T]) error, rejected func(*cooperative.Scope, error) error) error {
	if err := iterator.canSuspend(scope); err != nil {
		return err
	}
	if inner == nil || inner.AsyncIteratorOwner() != iterator.owner || done == nil {
		return cooperative.ErrRestricted
	}
	if own, ok := inner.(*OwnerIterator[T]); ok && own == iterator {
		return cooperative.ErrRestricted
	}
	var awaited *OwnerFuture[IteratorResult[T]]
	var err error
	if request.Throwing {
		thrower, ok := inner.(interface {
			Throw(*cooperative.Scope, error) (*OwnerFuture[IteratorResult[T]], error)
		})
		if !ok {
			return cooperative.ErrRestricted
		} // Missing-throw AsyncFromSync closing is not in this admitted graph.
		awaited, err = thrower.Throw(scope, request.Failure)
	} else if request.Returning {
		if request.Defined {
			awaited, err = inner.Return(scope, request.Value)
		} else {
			awaited, err = inner.ReturnUndefined(scope)
		}
	} else {
		if received, ok := inner.(interface {
			NextUndefined(*cooperative.Scope) (*OwnerFuture[IteratorResult[T]], error)
		}); ok {
			awaited, err = received.NextUndefined(scope)
		} else {
			awaited, err = inner.Next(scope)
		}
	}
	if err != nil {
		return err
	}
	return AwaitGenerator(scope, iterator, awaited, func(next *cooperative.Scope, result IteratorResult[T]) error {
		if result.Done {
			return done(next, result, request)
		}
		// This finite outer producer has a defined-value yield API. Keep an
		// unported yielded undefined distinct from a defined nil/zero value.
		if !result.Defined {
			return cooperative.ErrRestricted
		}
		return iterator.Yield(next, result.Value, func(later *cooperative.Scope, _ *OwnerIterator[T], pending IteratorRequest[T]) error {
			return iterator.Delegate(later, inner, pending, done, rejected)
		})
	}, rejected)
}

// Abandon retires one original continuation on a fresh same-owner lifetime
// Scope. It rejects obligations, never runs finally/history/model business.
// Late pending yield/await reactions cannot resurrect the retired continuation.
func (iterator *OwnerIterator[T]) Abandon(scope *cooperative.Scope, failure error) error {
	if failure == nil {
		return cooperative.ErrRestricted
	}
	if err := iterator.check(scope); err != nil {
		return err
	}
	iterator.generation++
	if iterator.active != nil {
		_ = iterator.active.future.RejectAdministrative(failure)
	}
	for _, request := range iterator.queue {
		_ = request.future.RejectAdministrative(failure)
	}
	iterator.active = nil
	iterator.queue = nil
	iterator.resume = nil
	iterator.executing = false
	iterator.awaiting = false
	iterator.held = false
	iterator.completed = true
	return nil
}
