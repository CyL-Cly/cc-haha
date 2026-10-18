package sdkhost

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelyield"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

// consumeWebModel retains Source's real async-generator delegation and every
// Next reaction even though WebFetch exposes only its final text to the caller.
func (h *Host) consumeWebModel(ctx context.Context, scope *cooperative.Scope, input requestretry.Input, receive func(modelyield.Yield) error, promptAuthority ...*cooperative.NativePromptHookModels) error {
	deps := h.requestDependencies(scope)
	deps.NativeWebSource = input.Context.Options.QuerySource
	if len(promptAuthority) == 1 && promptAuthority[0].Owns(h.options.Owner) {
		deps.NativePromptHookSource = promptAuthority[0]
	}
	_, e := modelyield.RunQueued(ctx, input, modelyield.QueuedDependencies{OnNativeQuota: h.nativeQuotaConsumer(), Request: deps, Activity: h.options.Activity, Session: h.sessionAccounting, AgentContext: h.agentContext, StreamClock: h.streamClock, StreamPolicy: h.streamPolicy, SuccessDuration: &modelaccounting.SuccessDuration{NowMillis: h.options.NowMillis, GetPermission: h.webModelPermission, Ignored: h.permissionRejections}, Start: func(first *cooperative.Scope, it *modelyield.QueryIterator, _ *modelyield.ResourceControl) (*ownerasync.OwnerFuture[struct{}], error) {
		done, e := ownerasync.NewOwnerFuture[struct{}](h.options.Owner)
		if e != nil {
			return nil, e
		}
		var demand func(*cooperative.Scope) error
		demand = func(next *cooperative.Scope) error {
			pending, e := it.Next(next)
			if e != nil {
				return done.Reject(next, e)
			}
			_, e = pending.Then(next, func(callback *cooperative.Scope, result modelyield.QueryResult, failure error) error {
				if failure != nil {
					return done.Reject(callback, failure)
				}
				if result.Done {
					return done.Resolve(callback, struct{}{})
				}
				if result.Value.Yield != nil {
					if e := receive(*result.Value.Yield); e != nil {
						return done.Reject(callback, e)
					}
				}
				return demand(callback)
			})
			return e
		}
		if e = demand(first); e != nil {
			return nil, e
		}
		return done, nil
	}})
	return e
}
