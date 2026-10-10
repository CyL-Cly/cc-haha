package builtinwrite

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

// native starts pure IO over immutable arguments. It borrows the original
// Writer's reference ledger and settles on the same Owner, never on the worker.
func native[T any](s *Service, scope *cooperative.Scope, work func() (T, error)) (*ownerasync.OwnerFuture[T], error) {
	if err := s.check(scope); err != nil {
		return nil, err
	}
	ref, err := s.options.NativeBinding.Acquire(scope)
	if err != nil {
		return nil, err
	}
	future, err := ownerasync.NewOwnerFuture[T](s.options.Owner)
	if err != nil {
		ref.DeliveryFinished()
		return nil, err
	}
	go func() {
		value, failure := work()
		receipt, deliveryError := s.options.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
			var e error
			if failure != nil {
				e = future.Reject(next, failure)
			} else {
				e = future.Resolve(next, value)
			}
			if e != nil {
				_ = future.RejectAdministrative(e)
			}
			release, e := s.options.Owner.Microtask(cooperative.Read, func(*cooperative.Scope) error { ref.DeliveryFinished(); return nil })
			if e != nil {
				ref.DeliveryFinished()
			} else {
				ref.ReclaimRejectedReceipt(release)
			}
			return nil
		})
		if deliveryError == nil {
			deliveryError = receipt.Wait()
		}
		if deliveryError != nil {
			_ = future.RejectAdministrative(deliveryError)
			ref.DeliveryFinished()
		}
	}()
	return future, nil
}
