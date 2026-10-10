package sdkhost

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func (b *NativeSyntheticOutput) start(ctx context.Context, input protocol.JSONObject, call queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
	scope := (*cooperative.Scope)(nil)
	if original, ok := call.Opaque.(*builtinwrite.CallContext); ok && original != nil {
		scope = original.Scope
	}
	if scope == nil || !scope.BelongsTo(b.owner) || !b.binding.Owns(b.owner) {
		return nil, cooperative.ErrRestricted
	}
	ref, e := b.binding.Acquire(scope)
	if e != nil {
		return nil, e
	}
	future, e := ownerasync.NewOwnerFuture[queryloop.ToolCallResult](b.owner)
	if e != nil {
		ref.DeliveryFinished()
		return nil, e
	}
	captured := input.Clone()
	go func() {
		cause := b.validate(ctx, captured)
		receipt, e := b.owner.SubmitIndependent(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error {
			var e error
			if cause != nil {
				e = future.Reject(scope, cause)
			} else {
				e = future.Resolve(scope, queryloop.ToolCallResult{Data: quote("Structured output provided successfully"), StructuredOutput: raw(captured)})
			}
			if e != nil {
				_ = future.RejectAdministrative(e)
			}
			cleanup, e := b.owner.Microtask(cooperative.Read, func(*cooperative.Scope) error { ref.DeliveryFinished(); return nil })
			if e != nil {
				ref.DeliveryFinished()
			} else {
				ref.ReclaimRejectedReceipt(cleanup)
			}
			return nil
		})
		if e == nil {
			e = receipt.Wait()
		}
		if e != nil {
			_ = future.RejectAdministrative(e)
			ref.DeliveryFinished()
		}
	}()
	return future, nil
}
