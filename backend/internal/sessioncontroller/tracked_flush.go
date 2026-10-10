package sessioncontroller

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// TrackedWrite starts only at Source's actual trackWrite admission. The outer
// Record/cache/discard await is not tracked. All counter/resolver mutations and
// the finally completion run on the same canonical owner as the Writer.
type TrackedWrite struct {
	pending *PendingWriter
	active  bool
}

func (pending *PendingWriter) BeginTrackedWrite(scope *cooperative.Scope) (*TrackedWrite, error) {
	if !scope.BelongsTo(pending.owner) {
		return nil, cooperative.ErrRestricted
	}
	var token *TrackedWrite
	err := scope.Inline(cooperative.Read, func(*cooperative.Scope) error {
		if pending.closing {
			return ErrPendingClosed
		}
		pending.tracked++
		token = &TrackedWrite{pending: pending, active: true}
		return nil
	})
	return token, err
}

// Complete is the finally decrement, including failed insert/enqueue work.
// Flush observes completion, not that operation's return value or error.
func (token *TrackedWrite) Complete(scope *cooperative.Scope) error {
	if token == nil || token.pending == nil || !scope.BelongsTo(token.pending.owner) {
		return cooperative.ErrRestricted
	}
	return scope.Inline(cooperative.Flush, func(*cooperative.Scope) error {
		if !token.active {
			return nil
		}
		token.active = false
		token.pending.tracked--
		if token.pending.tracked == 0 {
			waiters := token.pending.zeroWaiters
			token.pending.zeroWaiters = nil
			for _, future := range waiters {
				future.settle(nil)
			}
		}
		return nil
	})
}

// FlushFuture is separate from an outer Record future and the physical drain.
// Observer cancellation does not cancel the admitted flush or its native jobs.
type FlushFuture struct {
	future *WriteFuture
	owner  *cooperative.Owner
}

func (future *FlushFuture) Done() <-chan struct{}          { return future.future.Done() }
func (future *FlushFuture) Wait(ctx context.Context) error { return future.future.Wait(ctx) }
func (future *FlushFuture) Await(scope *cooperative.Scope) error {
	if !scope.BelongsTo(future.owner) {
		return cooperative.ErrRestricted
	}
	return future.future.Await(scope)
}

// Flush mirrors Project.flush's physical drain -> tracked-zero -> late enqueue
// loop. Begin remains the existing physical drain API; natural timer drains do
// not implicitly join unrelated tracked writers. This does not add tracking to
// the unimplemented outer Record or change its shutdown admission policy.
func (pending *PendingWriter) Flush(scope *cooperative.Scope) (*FlushFuture, error) {
	if !scope.BelongsTo(pending.owner) {
		return nil, cooperative.ErrRestricted
	}
	var future *FlushFuture
	err := scope.Inline(cooperative.Flush, func(scope *cooperative.Scope) error {
		if pending.closing {
			return ErrPendingClosed
		}
		future = &FlushFuture{future: newWriteFuture(), owner: pending.owner}
		job := &flushContinuation{pending: pending, future: future.future}
		job.drain(scope)
		return nil
	})
	return future, err
}

type flushContinuation struct {
	pending *PendingWriter
	future  *WriteFuture
}

func (job *flushContinuation) drain(scope *cooperative.Scope) {
	// Source clears the scheduled timer before awaiting an existing active
	// drain, including a timer installed by a late enqueue during that drain.
	job.pending.writer.mu.Lock()
	if job.pending.writer.stop != nil {
		job.pending.writer.stop()
		job.pending.writer.stop = nil
	}
	job.pending.writer.mu.Unlock()
	drain, err := job.pending.Begin(scope)
	if err != nil {
		job.future.settle(err)
		return
	}
	job.wait(drain, job.afterDrain)
}

// wait only observes immutable promise settlement or administrative lifetime.
// In particular a forced Owner.Close must not strand a zero-count observer.
func (job *flushContinuation) wait(waiting *WriteFuture, complete func(*cooperative.Scope)) {
	_ = waiting.thenOwner(job.pending.owner, func(scope *cooperative.Scope, err error) {
		if err != nil {
			job.future.settle(err)
			return
		}
		complete(scope)
	}, job.future.settle)
}
func (job *flushContinuation) afterDrain(scope *cooperative.Scope) {
	// This callback is already on owner. Use a fresh named owner callback for
	// the next drain; a Scope is never captured by or sent to a worker.
	if job.pending.tracked > 0 {
		zero := newWriteFuture()
		job.pending.zeroWaiters = append(job.pending.zeroWaiters, zero)
		job.wait(zero, job.drain)
		return
	}
	job.pending.writer.mu.Lock()
	remaining := len(job.pending.writer.queues) > 0 || job.pending.writer.stop != nil
	job.pending.writer.mu.Unlock()
	if job.pending.active == nil && !remaining {
		job.future.settle(nil)
		return
	}
	job.drain(scope)
}
