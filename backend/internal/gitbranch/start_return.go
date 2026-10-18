package gitbranch

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

// Source ensureStarted is async: both its first `return this.initPromise` and
// a concurrent caller's same return adopt the actual start Promise. Each caller
// owns a distinct returned Promise. The initialized no-value return remains
// fulfilled at its original synchronous return in ensureWithCWD.
func (s *Service) adoptStartReturn(scope *cooperative.Scope, actual *Future) *Future {
	returned := s.future()
	if scope == nil || !scope.BelongsTo(s.options.Owner) || actual == nil || actual.service != s {
		returned.reject(cooperative.ErrRestricted)
		return returned
	}
	s.workers.Add(1)
	receipt, e := s.options.Owner.Microtask(cooperative.Read, func(next *cooperative.Scope) error {
		return actual.register(next, reaction{
			run:      func(next *cooperative.Scope, value result) { s.settle(next, returned, value) },
			rejected: returned.reject,
		})
	})
	if e != nil {
		s.workers.Done()
		returned.reject(e)
		return returned
	}
	go func() {
		defer s.workers.Done()
		if e := receipt.Wait(); e != nil {
			// This observes administrative rejection only. No start/cache/watch
			// operation runs outside the canonical Owner.
			returned.reject(e)
		}
	}()
	return returned
}
