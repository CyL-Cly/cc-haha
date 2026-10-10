package ownerasync

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

// IteratorValue preserves undefined independently from a defined zero, nil or
// empty value. PromiseValue represents a native same-owner promise, not an
// arbitrary JS thenable or a value reconstructed from Source output.
type IteratorValue[T any] struct {
	Defined bool
	Value   T
}

// SyncIteratorResult is the plain result returned immediately by a synchronous
// producer. A nonnil PromiseValue is its original promise-valued .value.
type SyncIteratorResult[T any] struct {
	IteratorResult[T]
	PromiseValue *OwnerFuture[IteratorValue[T]]
}

// SyncNextRequest keeps next() distinct from the next(undefined) forwarded by
// async yield*. Next(value) for any defined value is outside this finite API.
type SyncNextRequest struct{ ArgumentPresent bool }

// SyncReturnRequest keeps return() distinct from return(undefined). Rejection
// closing has no argument; an explicit abrupt return always supplies one.
type SyncReturnRequest[T any] struct {
	ArgumentPresent bool
	Defined         bool
	Value           T
}

// OwnerSyncIterator methods execute synchronously on the admitted owner. Return
// receives the original abrupt value/undefined; there is no async generator
// demand queue, yield-await, provider worker or implicit business cleanup here.
type OwnerSyncIterator[T any] interface {
	SyncIteratorOwner() *cooperative.Owner
	NextSync(*cooperative.Scope, SyncNextRequest) (SyncIteratorResult[T], error)
	ReturnSync(*cooperative.Scope, SyncReturnRequest[T]) (SyncIteratorResult[T], error)
}

// AsyncFromSync implements the actual async-from-sync value-await/result-promise
// seam. The outer async generator separately awaits this result and yields.
type AsyncFromSync[T any] struct {
	owner *cooperative.Owner
	sync  OwnerSyncIterator[T]
}

func NewAsyncFromSync[T any](owner *cooperative.Owner, producer OwnerSyncIterator[T]) (*AsyncFromSync[T], error) {
	if owner == nil || producer == nil || producer.SyncIteratorOwner() != owner {
		return nil, cooperative.ErrRestricted
	}
	return &AsyncFromSync[T]{owner: owner, sync: producer}, nil
}

func (iterator *AsyncFromSync[T]) AsyncIteratorOwner() *cooperative.Owner {
	if iterator == nil {
		return nil
	}
	return iterator.owner
}

func (iterator *AsyncFromSync[T]) check(scope *cooperative.Scope) error {
	if iterator == nil || !scope.BelongsTo(iterator.owner) {
		return cooperative.ErrRestricted
	}
	return scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil })
}

func (iterator *AsyncFromSync[T]) Next(scope *cooperative.Scope) (*OwnerFuture[IteratorResult[T]], error) {
	return iterator.invoke(scope, IteratorRequest[T]{}, false)
}

// NextUndefined is the received-undefined request made by async yield*.
func (iterator *AsyncFromSync[T]) NextUndefined(scope *cooperative.Scope) (*OwnerFuture[IteratorResult[T]], error) {
	return iterator.invoke(scope, IteratorRequest[T]{}, true)
}

func (iterator *AsyncFromSync[T]) Return(scope *cooperative.Scope, value T) (*OwnerFuture[IteratorResult[T]], error) {
	return iterator.invoke(scope, IteratorRequest[T]{Returning: true, Defined: true, Value: value}, true)
}

func (iterator *AsyncFromSync[T]) ReturnUndefined(scope *cooperative.Scope) (*OwnerFuture[IteratorResult[T]], error) {
	return iterator.invoke(scope, IteratorRequest[T]{Returning: true}, true)
}

func (iterator *AsyncFromSync[T]) invoke(scope *cooperative.Scope, request IteratorRequest[T], argumentPresent bool) (*OwnerFuture[IteratorResult[T]], error) {
	if err := iterator.check(scope); err != nil {
		return nil, err
	}
	future, _ := NewOwnerFuture[IteratorResult[T]](iterator.owner)
	var result SyncIteratorResult[T]
	err := scope.Inline(cooperative.Read, func(next *cooperative.Scope) error {
		var err error
		if request.Returning {
			result, err = iterator.sync.ReturnSync(next, SyncReturnRequest[T]{ArgumentPresent: argumentPresent, Defined: request.Defined, Value: request.Value})
		} else {
			result, err = iterator.sync.NextSync(next, SyncNextRequest{ArgumentPresent: argumentPresent})
		}
		return err
	})
	if err != nil {
		return future, future.Reject(scope, err)
	}
	value := result.PromiseValue
	if value == nil {
		value, _ = NewOwnerFuture[IteratorValue[T]](iterator.owner)
		_ = value.Resolve(scope, IteratorValue[T]{Defined: result.Defined, Value: result.Value})
	} else if value.owner != iterator.owner || !result.Defined {
		return future, future.Reject(scope, cooperative.ErrRestricted)
	}
	// PromiseResolve uses an existing native Promise directly. A literal value,
	// including done=true/undefined, still receives this actual Then continuation.
	_, err = value.Then(scope, func(next *cooperative.Scope, resolved IteratorValue[T], failure error) error {
		if failure != nil {
			if !result.Done && !request.Returning {
				// Next closes a sync producer when an originally yielded value
				// rejects. An abrupt completion retains its original rejection identity.
				var zero T
				_ = next.Inline(cooperative.Read, func(closing *cooperative.Scope) error {
					_, err := iterator.sync.ReturnSync(closing, SyncReturnRequest[T]{Value: zero})
					return err
				})
			}
			return future.Reject(next, failure)
		}
		return future.Resolve(next, IteratorResult[T]{Done: result.Done, Defined: resolved.Defined, Value: resolved.Value})
	})
	return future, err
}
