package sessioncontroller

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

// Native runtime references exclude pure Promise/Branch observers and tracked
// writes. They cover only actual dispatched transcript stat/read/open/append/
// close work through delivery of its same-owner completion receipt.
type runtimeNativeReferences struct{ refs ownerasync.NativeReferences }

func (refs *runtimeNativeReferences) begin()                           { refs.refs.Begin() }
func (refs *runtimeNativeReferences) end()                             { refs.refs.End() }
func (refs *runtimeNativeReferences) snapshot() (int, <-chan struct{}) { return refs.refs.Snapshot() }

// WaitRuntimeNative is an off-owner process-liveness checkpoint, not Close or
// Flush. The independent owner command is behind all admitted FIFO promise
// reactions, so a native completion which dispatches another I/O keeps its
// reference alive. A pure unresolved Promise does not prevent this returning.
// Only this Writer's native jobs are covered; other host runtime refs need the
// canonical process registry before a full CLI/Server process exit claim.
func (pending *PendingWriter) WaitRuntimeNative(ctx context.Context) error {
	if pending == nil || pending.owner == nil || ctx == nil {
		return cooperative.ErrRestricted
	}
	for {
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}
		var count int
		var idle <-chan struct{}
		receipt, err := pending.owner.SubmitIndependent(ctx, cooperative.Flush, func(*cooperative.Scope) error { count, idle = pending.runtimeNative.snapshot(); return nil })
		if err != nil {
			return err
		}
		if err = receipt.Wait(); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}
		if count == 0 {
			return nil
		}
		select {
		case <-idle:
		case <-pending.owner.Done():
			return cooperative.ErrClosed
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}
