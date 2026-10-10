package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

type OwnerFuture[T any] = ownerasync.OwnerFuture[T]

func NewOwnerFuture[T any](owner *cooperative.Owner) (*OwnerFuture[T], error) {
	return ownerasync.NewOwnerFuture[T](owner)
}
