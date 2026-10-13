package resumestate

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
)

func (s *Service) beginOwnedContinue(scope *cooperative.Scope, resumeAt string) (*ownerasync.OwnerFuture[*Result], error) {
	out, err := ownerasync.NewOwnerFuture[*Result](s.owned.state.Owner())
	if err != nil {
		return nil, err
	}
	fail := func(next *cooperative.Scope, failure error) {
		s.options.OnError(failure)
		resolveOwned(out, next, (*Result)(nil), failure)
	}
	ref, err := s.owned.binding.Acquire(scope)
	if err != nil {
		return nil, err
	}
	files, directory, statFile := s.options.Files, s.projectDir(), s.options.Stat
	owner := s.owned.state.Owner()
	go func() {
		inventory := ReadContinueInventory(files, directory, statFile)
		receipt, failure := owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
			log, failure := s.selectContinueInventory(inventory)
			if failure != nil {
				fail(next, failure)
			} else if !log.IsObject() {
				resolveOwned(out, next, (*Result)(nil), nil)
			} else {
				read, failure := s.beginSIDRead(next, text(log.Get("fullPath")))
				if failure != nil {
					fail(next, failure)
				} else {
					_, failure = read.Then(next, func(after *cooperative.Scope, transcript *sessionhistory.Transcript, failure error) error {
						if failure != nil {
							fail(after, failure)
							return nil
						}
						full, failure := s.fullLogFromTranscript(log, transcript, object(encode(transcript)))
						if failure != nil {
							fail(after, failure)
							return nil
						}
						original, failure := DecodeOriginalLog(full)
						if failure != nil {
							fail(after, failure)
							return nil
						}
						s.proceedOwnedOriginal(after, original, out, fail, resumeAt)
						return nil
					})
					if failure != nil {
						fail(next, failure)
					}
				}
			}
			release, failure := owner.Microtask(cooperative.Read, func(*cooperative.Scope) error { ref.DeliveryFinished(); return nil })
			if failure == nil {
				ref.ReclaimRejectedReceipt(release)
			} else {
				ref.DeliveryFinished()
			}
			return nil
		})
		if failure == nil {
			failure = receipt.Wait()
		}
		if failure != nil {
			_ = out.RejectAdministrative(failure)
			ref.DeliveryFinished()
		}
	}()
	return out, nil
}
