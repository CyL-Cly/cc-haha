package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

type IteratorResult[T any] = ownerasync.IteratorResult[T]
type IteratorRequest[T any] = ownerasync.IteratorRequest[T]
type OwnerAsyncIterator[T any] = ownerasync.OwnerAsyncIterator[T]
type GeneratorContinuation[T any] = ownerasync.GeneratorContinuation[T]
type OwnerIterator[T any] = ownerasync.OwnerIterator[T]

func NewOwnerIterator[T any](owner *cooperative.Owner, start GeneratorContinuation[T]) (*OwnerIterator[T], error) {
	return ownerasync.NewOwnerIterator(owner, start)
}
func AwaitGenerator[T, U any](scope *cooperative.Scope, iterator *OwnerIterator[T], awaited *OwnerFuture[U], run func(*cooperative.Scope, U) error, rejected func(*cooperative.Scope, error) error) error {
	return ownerasync.AwaitGenerator(scope, iterator, awaited, run, rejected)
}
