package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
)

// Physical Worktree operations run on the existing native worker. Their hook
// transport belongs to the parent print Owner and is admitted there as a Read
// continuation; this bridge copies only its immutable return value.
func sdkWorktreeOwnerCall[T any](ctx context.Context, m *MainPrint, run func(*cooperative.Scope) (T, error)) (T, error) {
	var zero T
	future, err := ownerasync.NewOwnerFuture[T](m.Owner)
	if err != nil {
		return zero, err
	}
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		value, e := run(scope)
		if e != nil {
			return future.Reject(scope, e)
		}
		return future.Resolve(scope, value)
	})
	if err != nil {
		return zero, err
	}
	// A rejected admission/Owner close must settle the physical worker as well.
	go func() {
		if e := receipt.Wait(); e != nil {
			_ = future.RejectAdministrative(e)
		}
	}()
	return future.Wait(ctx)
}
func (m *MainPrint) nativeWorktreeHasCreateHook(ctx context.Context) (bool, error) {
	return sdkWorktreeOwnerCall(ctx, m, func(scope *cooperative.Scope) (bool, error) {
		return m.hook.HasWorktreeCreateHooks(m.state.SessionID())
	})
}
func (m *MainPrint) nativeWorktreeCreateHook(ctx context.Context, name string) (string, error) {
	return sdkWorktreeOwnerCall(ctx, m, func(scope *cooperative.Scope) (string, error) {
		trusted, e := m.nativeHookTrustAccepted(scope)
		if e != nil {
			return "", e
		}
		return m.hook.ExecuteWorktreeCreateHook(ctx, name, m.state.SessionID(), queryhooks.Context{Scope: scope, Interactive: m.options.Interactive, TrustAccepted: trusted})
	})
}
func (m *MainPrint) nativeWorktreeRemoveHook(ctx context.Context, path string) (bool, error) {
	return sdkWorktreeOwnerCall(ctx, m, func(scope *cooperative.Scope) (bool, error) {
		trusted, e := m.nativeHookTrustAccepted(scope)
		if e != nil {
			return false, e
		}
		return m.hook.ExecuteWorktreeRemoveHook(ctx, path, m.state.SessionID(), queryhooks.Context{Scope: scope, Interactive: m.options.Interactive, TrustAccepted: trusted})
	})
}
func (m *MainPrint) nativeHookTrustAccepted(scope *cooperative.Scope) (bool, error) {
	// Noninteractive print has Source's outside-REPL trust exemption. Interactive
	// callers use the existing accepted project trust producer.
	if !m.options.Interactive {
		return true, nil
	}
	return m.nativeGoalTrust(scope)
}

func (m *MainPrint) bindNativeSDKChildCallbacks(scope *cooperative.Scope) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	d := m.options.DetachedAgent
	if d == nil {
		return nil
	}
	parent := d.authParent
	if parent == nil && d.preset != nil {
		parent = d.preset.parent
	}
	if parent == nil {
		return nil
	}
	root := parent.hook.NativeSDKCallbackBindings(parent.Owner)
	if root == nil {
		return nil
	}
	return m.hook.BindNativeSDKCallbacks(scope, &queryhooks.CallbackBindings{Owner: m.Owner, Store: root.Store, Run: func(ctx context.Context, call queryhooks.Context, hook, input protocol.JSONObject) (protocol.JSONObject, error) {
		copiedHook, copiedInput := hook.Clone(), input.Clone()
		return nativeAgentOwnerCall(m, call.Scope, parent, func(next *cooperative.Scope) (protocol.JSONObject, error) {
			current := call
			current.Scope = next
			value, e := root.Run(ctx, current, copiedHook, copiedInput)
			return value.Clone(), e
		})
	}})
}
