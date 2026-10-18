package sessioncontroller

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/gitbranch"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

type ownedRecordBranchReturn struct {
	future *ownerasync.OwnerFuture[string]
}

func (future *ownedRecordBranchReturn) Done() <-chan struct{} { return future.future.Done() }
func (future *ownedRecordBranchReturn) Wait(ctx context.Context) (string, error) {
	return future.future.Wait(ctx)
}
func (future *ownedRecordBranchReturn) Await(scope *cooperative.Scope) (string, error) {
	return future.future.Await(scope)
}
func (future *ownedRecordBranchReturn) ThenOwner(scope *cooperative.Scope, run func(*cooperative.Scope, string, error), onClosed func(error)) error {
	if future == nil || future.future == nil || run == nil || onClosed == nil {
		return cooperative.ErrRestricted
	}
	reaction, err := future.future.Then(scope, func(next *cooperative.Scope, value string, failure error) error {
		run(next, value, failure)
		return nil
	})
	if err == nil {
		// Only the rejected receipt is observed off Owner; business callbacks
		// remain actual Owner reactions, including administrative close.
		go func() {
			if _, failure := reaction.Wait(context.Background()); failure != nil {
				onClosed(failure)
			}
		}()
	}
	return err
}

// Source utils/git.ts getBranch is async and returns getCachedBranch's exact
// Promise. The Git service already implements that inner watcher/cache Promise;
// this caller alone owns the outer return-adoption job and forwarding reaction.
func beginOwnedRecordBranchReturn(scope *cooperative.Scope, owner *cooperative.Owner, actual *gitbranch.BranchFuture) (RecordBranchFuture, error) {
	if owner == nil || scope == nil || !scope.BelongsTo(owner) || actual == nil || actual.Future == nil {
		return nil, cooperative.ErrRestricted
	}
	returned, err := ownerasync.NewOwnerFuture[string](owner)
	if err != nil {
		return nil, err
	}
	_, err = owner.Microtask(cooperative.Read, func(next *cooperative.Scope) error {
		err := actual.ThenOwner(next, func(next *cooperative.Scope, value string, failure error) {
			if failure != nil {
				_ = returned.Reject(next, failure)
			} else {
				_ = returned.Resolve(next, value)
			}
		}, func(failure error) { _ = returned.RejectAdministrative(failure) })
		if err != nil {
			return returned.Reject(next, err)
		}
		return nil
	})
	if err != nil {
		_ = returned.RejectAdministrative(err)
		return nil, err
	}
	return &ownedRecordBranchReturn{future: returned}, nil
}
