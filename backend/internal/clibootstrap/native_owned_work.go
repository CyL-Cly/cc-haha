package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

// Physical work is joined through the existing owner-local future and binding.
func nativeOwnedWork[T any](owner *cooperative.Owner, binding *ownerasync.NativeBinding, scope *cooperative.Scope, work func() (T, error)) (*ownerasync.OwnerFuture[T], error) {
	if !scope.BelongsTo(owner) || !binding.Owns(owner) {
		return nil, cooperative.ErrRestricted
	}
	ref, e := binding.Acquire(scope)
	if e != nil {
		return nil, e
	}
	future, e := ownerasync.NewOwnerFuture[T](owner)
	if e != nil {
		ref.DeliveryFinished()
		return nil, e
	}
	go func() {
		value, failure := work()
		receipt, e := owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
			var e error
			if failure != nil {
				e = future.Reject(next, failure)
			} else {
				e = future.Resolve(next, value)
			}
			if e != nil {
				_ = future.RejectAdministrative(e)
			}
			r, e := owner.Microtask(cooperative.Read, func(*cooperative.Scope) error { ref.DeliveryFinished(); return nil })
			if e != nil {
				ref.DeliveryFinished()
			} else {
				ref.ReclaimRejectedReceipt(r)
			}
			return nil
		})
		if e == nil {
			e = receipt.Wait()
		}
		if e != nil {
			_ = future.RejectAdministrative(e)
			ref.DeliveryFinished()
		}
	}()
	return future, nil
}
