package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
)

// BeginQueueOperation retains recordQueueOperation -> async insertQueueOperation
// -> trackWrite(async () => await appendEntry). The real append keeps its own
// admission/native work. Its caller's async return, finally, return adoption
// and outer await are distinct Promise reactions even for a buffered entry.
func (pending *PendingWriter) BeginQueueOperation(scope *cooperative.Scope, loader *sessionhistory.Loader, entry *protocol.JSONObject) (*WriteFuture, error) {
	if pending == nil || pending.owner == nil || scope == nil || !scope.BelongsTo(pending.owner) || entry == nil || entry.String("type") != "queue-operation" || loader == nil {
		return nil, cooperative.ErrRestricted
	}
	token, err := pending.BeginTrackedWrite(scope)
	if err != nil {
		return nil, err
	}
	append, err := pending.BeginAppend(scope, loader, entry, "")
	if err != nil {
		_ = token.Complete(scope)
		return nil, err
	}
	owner := pending.owner
	body, _ := ownerasync.NewOwnerFuture[struct{}](owner)
	track, _ := ownerasync.NewOwnerFuture[struct{}](owner)
	insertion, _ := ownerasync.NewOwnerFuture[struct{}](owner)
	returned := newWriteFuture()
	// Administrative close only rejects the immutable external observation.
	// It must not run Source finally or mutate the pending counter off Owner.
	go func() {
		select {
		case <-returned.Done():
		case <-owner.Done():
			returned.settle(cooperative.ErrClosed)
		}
	}()
	if _, err = body.Then(scope, func(next *cooperative.Scope, _ struct{}, failure error) error {
		if completed := token.Complete(next); failure == nil {
			failure = completed
		}
		if failure != nil {
			return track.Reject(next, failure)
		}
		return track.Resolve(next, struct{}{})
	}); err != nil {
		returned.settle(err)
		return nil, err
	}
	if _, err = insertion.Then(scope, func(_ *cooperative.Scope, _ struct{}, failure error) error { returned.settle(failure); return nil }); err != nil {
		returned.settle(err)
		return nil, err
	}
	if err = append.thenOwner(owner, func(next *cooperative.Scope, failure error) {
		if failure != nil {
			if e := body.Reject(next, failure); e != nil {
				returned.settle(e)
			}
		} else if e := body.Resolve(next, struct{}{}); e != nil {
			returned.settle(e)
		}
	}, returned.settle); err != nil {
		returned.settle(err)
		return nil, err
	}
	// async insertQueueOperation invokes its body before returning trackWrite.
	if err = insertion.ResolveFrom(scope, track); err != nil {
		returned.settle(err)
		return nil, err
	}
	return returned, nil
}
