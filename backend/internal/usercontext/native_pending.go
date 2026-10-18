package usercontext

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// BeginUserContextOnOwner retains the original mode resolver and memo identity.
// A waiter never owns its producer's signal. lifetime is the factory's session
// prompt lifetime; files exposes only per-operation native filesystem work.
func (s *Service) BeginUserContextOnOwner(scope *cooperative.Scope, owner *cooperative.Owner, lanes *cooperative.NativePromptContinuations, lifetime context.Context, files func(*cooperative.Scope) NativeMemoryPrimitives) (*ownerasync.OwnerFuture[*protocol.JSONObject], error) {
	if s == nil || !scope.BelongsTo(owner) || !lanes.Owns(owner) || lifetime == nil || files == nil {
		return nil, cooperative.ErrRestricted
	}
	if err := lanes.CheckCaller(scope); err != nil {
		return nil, err
	}
	if err := lifetime.Err(); err != nil {
		return nil, context.Cause(lifetime)
	}
	f, memory, mode, directories := s.beginUserContext()
	s.mu.Lock()
	if f.native != nil {
		result, same := f.native, f.nativeOwner == owner
		s.mu.Unlock()
		if !same {
			return nil, cooperative.ErrRestricted
		}
		return result, nil
	}
	result, err := ownerasync.NewOwnerFuture[*protocol.JSONObject](owner)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	f.nativeOwner, f.native = owner, result
	s.mu.Unlock()
	if !f.Pending() {
		value, failure := f.Await(context.Background())
		if failure != nil {
			err = result.Reject(scope, failure)
		} else {
			err = result.Resolve(scope, value)
		}
		return result, err
	}
	err = lanes.Start(scope, lifetime, func(next *cooperative.Scope) error {
		if memory == nil {
			return next.AwaitPromise(f.done)
		}
		// Copy only the file capability. Normal/cache/approval descriptor identity
		// is deliberately shared with finite readers and other pending waiters.
		job := *memory
		job.Files = files(next)
		value, failure := job.load(mode, directories)
		if failure == nil {
			failure = context.Cause(lifetime)
		}
		s.mu.Lock()
		s.finishLocked(f, value, failure)
		s.mu.Unlock()
		return nil
	}, func(next *cooperative.Scope, failure error) {
		if failure != nil {
			// Administrative close cannot publish a late memory/date/cache commit.
			s.mu.Lock()
			if f.Pending() && memory != nil {
				f.err = failure
				close(f.done)
			}
			s.mu.Unlock()
			_ = result.Reject(next, failure)
			return
		}
		value, failure := f.Await(context.Background())
		if failure != nil {
			_ = result.Reject(next, failure)
		} else {
			_ = result.Resolve(next, value)
		}
	})
	if err != nil {
		if memory != nil {
			s.mu.Lock()
			if f.Pending() {
				f.err = err
				close(f.done)
			}
			s.mu.Unlock()
		}
		_ = result.RejectAdministrative(err)
	}
	return result, err
}
