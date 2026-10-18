package sessioncontroller

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

// loadSessionFile is async and returns loadTranscriptFile's actual Promise;
// getSessionMessages then awaits that separate returned Promise before creating
// its memoized Set. Neither reaction is filesystem work or a fairness yield.
func (job *uuidJob) beginLoadReturn(scope *cooperative.Scope) (*WriteFuture, error) {
	if job == nil || job.pending == nil || scope == nil || !scope.BelongsTo(job.pending.owner) || job.future == nil {
		return nil, cooperative.ErrRestricted
	}
	transcript, session := newWriteFuture(), newWriteFuture()
	if e := session.thenOwner(job.pending.owner, func(_ *cooperative.Scope, errorValue error) {
		if errorValue == nil {
			job.future.set = job.loaded.OwnedMessageSet(job.pending.owner)
		}
		job.future.future.settle(errorValue)
	}, job.future.future.settle); e != nil {
		return nil, e
	}
	// The async return-adoption job is registered at loadSessionFile's original
	// call, before the existing asynchronous stat/read finishes its body.
	job.pending.workers.Add(1)
	receipt, e := job.pending.owner.Microtask(cooperative.Flush, func(next *cooperative.Scope) error {
		return transcript.thenOwner(job.pending.owner, func(_ *cooperative.Scope, errorValue error) {
			session.settle(errorValue)
		}, session.settle)
	})
	if e != nil {
		job.pending.workers.Done()
		session.settle(e)
		return nil, e
	}
	go func() {
		defer job.pending.workers.Done()
		if e := receipt.Wait(); e != nil {
			// Only propagate administrative close rejection off Owner. Parsing,
			// Set creation and cache publication remain actual Owner reactions.
			session.settle(e)
		}
	}()
	return transcript, nil
}
