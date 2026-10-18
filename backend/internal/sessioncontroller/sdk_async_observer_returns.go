package sessioncontroller

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

// sdkAsyncReturnObservers represents the actual Source fixture's async spies,
// not ordinary persistence policy. It owns no payload, I/O or configurable job
// count. A nil binding preserves the existing production futures and observers.
type sdkAsyncReturnObservers struct {
	controller *Controller
	writer     *Writer
	owner      *cooperative.Owner
}

// BindSDKAsyncReturnObservers attaches the async return shape of the existing
// SDK observers to this actual Controller. Observation callbacks must already
// exist. It does not start a session, initialize Host, admit input or drain I/O.
func (c *Controller) BindSDKAsyncReturnObservers(scope *cooperative.Scope) error {
	if c == nil || c.writer == nil || c.options.OnSDKUUIDLookup == nil || c.options.OnSDKRecord == nil {
		return cooperative.ErrRestricted
	}
	var owner *cooperative.Owner
	if c.options.Bootstrap != nil {
		if err := c.options.Bootstrap.CheckScope(scope); err != nil {
			return err
		}
		owner = c.options.Bootstrap.Owner()
	} else if pending := c.writer.async; pending != nil {
		owner = pending.owner
	}
	if owner == nil || scope == nil || !scope.BelongsTo(owner) {
		return cooperative.ErrRestricted
	}
	if err := scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil }); err != nil {
		return err
	}
	if c.sdkAsyncObservers == nil {
		c.sdkAsyncObservers = &sdkAsyncReturnObservers{controller: c, writer: c.writer, owner: owner}
	}
	binding, err := c.sdkAsyncObserverBinding(scope)
	if err != nil {
		return err
	}
	if pending := c.writer.async; pending != nil {
		if pending.writer != c.writer || pending.owner != owner || pending.sdkAsyncObservers != nil && pending.sdkAsyncObservers != binding {
			return cooperative.ErrRestricted
		}
		pending.sdkAsyncObservers = binding
	}
	return nil
}

func (c *Controller) sdkAsyncObserverBinding(scope *cooperative.Scope) (*sdkAsyncReturnObservers, error) {
	if c == nil {
		return nil, cooperative.ErrRestricted
	}
	binding := c.sdkAsyncObservers
	if binding == nil {
		return nil, nil
	}
	if binding.controller != c || binding.writer != c.writer || binding.owner == nil || scope == nil || !scope.BelongsTo(binding.owner) || c.options.OnSDKUUIDLookup == nil || c.options.OnSDKRecord == nil {
		return nil, cooperative.ErrRestricted
	}
	if c.options.Bootstrap != nil && !c.options.Bootstrap.Owns(binding.owner) {
		return nil, cooperative.ErrRestricted
	}
	if pending := c.writer.async; pending != nil && pending.owner != binding.owner {
		return nil, cooperative.ErrRestricted
	}
	if err := scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil }); err != nil {
		return nil, err
	}
	return binding, nil
}

// recordReturn is the observer's `async (...args) => { observe(args); return
// originalRecord(...args) }`. The actual Record starts before this adoption
// job, and its result/error is forwarded without inspecting transcript state.
func (binding *sdkAsyncReturnObservers) recordReturn(scope *cooperative.Scope, original *RecordFuture) (*RecordFuture, error) {
	if binding == nil || original == nil || original.future == nil || original.owner != binding.owner {
		return nil, cooperative.ErrRestricted
	}
	actual, err := binding.controller.sdkAsyncObserverBinding(scope)
	if err != nil || actual != binding {
		return nil, cooperative.ErrRestricted
	}
	returned := &RecordFuture{future: newWriteFuture(), owner: binding.owner}
	// Administrative close only rejects an immutable observer obligation.
	go func() {
		select {
		case <-returned.Done():
		case <-binding.owner.Done():
			returned.future.settle(cooperative.ErrClosed)
		}
	}()
	_, err = binding.owner.Microtask(cooperative.Read, func(next *cooperative.Scope) error {
		return original.future.then(next, func(_ *cooperative.Scope, failure error) {
			returned.uuid = append(json.RawMessage(nil), original.uuid...)
			returned.future.settle(failure)
		})
	})
	if err != nil {
		returned.future.settle(err)
		return nil, err
	}
	return returned, nil
}

// WrapSDKFlushObserverReturn is the async observer's return of the exact real
// flushSessionStorage export R. Nil observers return R itself with no reaction.
func (c *Controller) WrapSDKFlushObserverReturn(scope *cooperative.Scope, original *ownerasync.OwnerFuture[struct{}]) (*ownerasync.OwnerFuture[struct{}], error) {
	binding, err := c.sdkAsyncObserverBinding(scope)
	if err != nil {
		return nil, err
	}
	if binding == nil {
		return original, nil
	}
	returned, err := ownerasync.NewOwnerFuture[struct{}](binding.owner)
	if err != nil {
		return nil, err
	}
	if err = returned.ResolveFrom(scope, original); err != nil {
		_ = returned.RejectAdministrative(err)
		return nil, err
	}
	return returned, nil
}
