package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

func (job *recordContinuation) beginInsertReturn(scope *cooperative.Scope) error {
	if job == nil || job.pending == nil || !scope.BelongsTo(job.pending.owner) || job.token == nil || job.token.pending != job.pending || job.future == nil || job.future.future == nil || job.future.owner != job.pending.owner || job.insertBody != nil {
		return cooperative.ErrRestricted
	}
	owner := job.pending.owner
	job.insertBody, _ = ownerasync.NewOwnerFuture[struct{}](owner)
	job.insertTrack, _ = ownerasync.NewOwnerFuture[struct{}](owner)
	job.insertReturn, _ = ownerasync.NewOwnerFuture[struct{}](owner)
	// Closing an Owner rejects the external observer obligation, without
	// running a finally/cache mutation on the administrative watcher.
	go func() {
		select {
		case <-job.future.Done():
		case <-owner.Done():
			job.future.future.settle(cooperative.ErrClosed)
		}
	}()
	if _, err := job.insertBody.Then(scope, func(next *cooperative.Scope, _ struct{}, failure error) error {
		if completed := job.token.Complete(next); failure == nil {
			failure = completed
		}
		job.token = nil
		if failure != nil {
			return job.insertTrack.Reject(next, failure)
		}
		return job.insertTrack.Resolve(next, struct{}{})
	}); err != nil {
		return err
	}
	_, err := job.insertReturn.Then(scope, func(next *cooperative.Scope, _ struct{}, failure error) error {
		job.finishResult(next, failure)
		return nil
	})
	return err
}

func (job *recordContinuation) adoptInsertReturn(scope *cooperative.Scope) {
	if err := job.insertReturn.ResolveFrom(scope, job.insertTrack); err != nil {
		job.future.future.settle(err)
	}
}
