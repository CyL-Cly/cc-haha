package resumestate

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

// nativeRead gives a worker only immutable syscall arguments. Decoding, plan
// decisions, settings/cache/diagnostic work happen in the caller's Owner reaction.
func (s *Service) nativeRead(scope *cooperative.Scope, path string) (*ownerasync.OwnerFuture[[]byte], error) {
	ref, e := s.owned.binding.Acquire(scope)
	if e != nil {
		return nil, e
	}
	out, e := ownerasync.NewOwnerFuture[[]byte](s.owned.state.Owner())
	if e != nil {
		ref.DeliveryFinished()
		return nil, e
	}
	read, owner := s.options.Files.ReadFile, s.owned.state.Owner()
	go func() {
		bytes, failure := read(path)
		receipt, e := owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
			resolveOwned(out, next, bytes, failure)
			release, e := owner.Microtask(cooperative.Read, func(*cooperative.Scope) error { ref.DeliveryFinished(); return nil })
			if e == nil {
				ref.ReclaimRejectedReceipt(release)
			}
			return e
		})
		if e == nil {
			e = receipt.Wait()
		}
		if e != nil {
			_ = out.RejectAdministrative(e)
			ref.DeliveryFinished()
		}
	}()
	return out, nil
}
