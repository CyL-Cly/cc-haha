package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

type IteratorValue[T any] = ownerasync.IteratorValue[T]
type SyncIteratorResult[T any] = ownerasync.SyncIteratorResult[T]
type SyncNextRequest = ownerasync.SyncNextRequest
type SyncReturnRequest[T any] = ownerasync.SyncReturnRequest[T]
type OwnerSyncIterator[T any] = ownerasync.OwnerSyncIterator[T]
type AsyncFromSync[T any] = ownerasync.AsyncFromSync[T]

func NewAsyncFromSync[T any](owner *cooperative.Owner, producer OwnerSyncIterator[T]) (*AsyncFromSync[T], error) {
	return ownerasync.NewAsyncFromSync(owner, producer)
}
