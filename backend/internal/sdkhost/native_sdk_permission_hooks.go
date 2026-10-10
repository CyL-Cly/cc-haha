package sdkhost

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

type nativePermissionHookWinner struct {
	sdk      bool
	decision *queryloop.ToolPermission
	response protocol.JSONObject
	failure  error
}

// This is StructuredIO.createCanUseTool's actual Promise.race consumer. Hook
// jobs retain the parent signal and keep their side effects when SDK wins;
// only a definitive hook winner cancels the separate SDK request.
func (h *Host) resolveNativeSDKPermissionHooks(ctx context.Context, scope *cooperative.Scope, tool queryloop.RegisteredTool, input *protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
	if h == nil || !scope.BelongsTo(h.options.Owner) || !h.nativePermissionHooks.Owns(h.options.Owner) || h.nativePermissionHookContext == nil {
		return queryloop.ToolPermission{}, cooperative.ErrRestricted
	}
	hookFuture, err := NewOwnerFuture[*queryloop.ToolPermission](h.options.Owner)
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	// Child permission bridges carry descriptors, never a foreign handler.
	// Match the original same-owner catalog before preparing Hook `if` rules.
	if tool.ParseInput == nil {
		for _, actual := range h.registered {
			if actual.Name == tool.Name {
				tool.ParseInput = actual.ParseInput
				break
			}
		}
		if tool.ParseInput == nil {
			return queryloop.ToolPermission{}, cap("actual parent Hook tool schema")
		}
	}
	c := h.nativeToolHookContext(scope, call, h.nativePermissionHookContext)
	if call.NativePermissionToolNames != nil {
		names := append([]string{}, call.NativePermissionToolNames...)
		c.PromptToolNames = &names
	}
	if child := sdkPermissionAgentID(call); child != "" {
		c.AgentID = child
	}
	sid := h.options.Controller.SessionID()
	if c.AgentID != "" {
		sid = c.AgentID
	}
	id, err := nativeHookToolID(call.ToolUseID)
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	fields := protocol.NewJSONObject(protocol.JSONField{Name: "tool_name", Value: quote(tool.Name)}, protocol.JSONField{Name: "tool_input", Value: raw(*input)})
	if call.PermDecision != nil {
		fields.Set("permission_suggestions", call.PermDecision.Suggestions)
	}
	if call.NativePermissionAgentType != "" {
		fields.Set("agent_type", quote(call.NativePermissionAgentType))
	}
	var verdict *queryloop.ToolPermission
	hookSignal := call.NativePermissionHookSignal
	if hookSignal == nil {
		return queryloop.ToolPermission{}, cooperative.ErrRestricted
	}
	err = h.nativePermissionHooks.Start(scope, hookSignal, func(next *cooperative.Scope, active context.Context) error {
		current := c
		current.Scope = next
		return h.hooks.ExecuteCommandEvent(active, queryhooks.CommandEvent{Event: "PermissionRequest", SessionID: sid, ToolUseID: id, MatchQuery: tool.Name, Fields: fields, Call: current, IfMatcher: nativeHookIfMatcher(tool, input)}, func(result queryhooks.EventResult) error {
			if verdict != nil {
				return nil
			}
			d, ok := result.Fields.Object("permissionRequestResult")
			if !ok || d.String("behavior") != "allow" && d.String("behavior") != "deny" {
				return nil
			}
			decision := queryloop.ToolPermission{Behavior: d.String("behavior"), Input: input, DecisionReason: raw(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("hook")}, protocol.JSONField{Name: "hookName", Value: quote("PermissionRequest")}))}
			if decision.Behavior == "allow" {
				decision.UpdatedInput = input
				if changed, ok := d.Object("updatedInput"); ok {
					decision.UpdatedInput = &changed
				}
				// Apply here, before publishing the hook's decision. These are the actual
				// late hook side effects even after the outer SDK race has returned.
				if updates := d.Get("updatedPermissions"); hookRawTruthy(updates) {
					if err := h.ApplyNativePermissionUpdates(next, updates); err != nil {
						return err
					}
				}
			} else {
				decision.Message = d.Get("message")
				if !hookRawTruthy(decision.Message) {
					decision.Message = quote("Permission denied by PermissionRequest hook")
				}
			}
			verdict = &decision
			return hookFuture.Resolve(next, verdict)
		})
	}, func(next *cooperative.Scope, failure error) {
		if failure != nil {
			_ = hookFuture.Reject(next, failure)
		} else {
			_ = hookFuture.Resolve(next, verdict)
		}
	})
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	prompt, err := h.BeginNativeSDKPermissionPrompt(ctx, scope, tool, input, call)
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	defer func() {
		if ctx.Err() != nil {
			// Parent abort can unwind the main await before its AfterFunc's owner
			// job. Keep that real producer pending until a fresh Cancel scope has
			// emitted the cancel packet and rejected its actual SDK Future.
			if _, e := h.options.Owner.SubmitIndependent(context.Background(), cooperative.Cancel, func(next *cooperative.Scope) error {
				if e := prompt.Cancel(next); e != nil {
					return e
				}
				return prompt.Finish(next)
			}); e == nil {
				return
			}
		}
		_ = prompt.Finish(scope)
	}()
	race, err := NewOwnerFuture[nativePermissionHookWinner](h.options.Owner)
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	if _, err = hookFuture.Then(scope, func(next *cooperative.Scope, d *queryloop.ToolPermission, e error) error {
		return race.Resolve(next, nativePermissionHookWinner{decision: d, failure: e})
	}); err != nil {
		return queryloop.ToolPermission{}, err
	}
	if _, err = prompt.RawFuture().Then(scope, func(next *cooperative.Scope, r protocol.JSONObject, e error) error {
		return race.Resolve(next, nativePermissionHookWinner{sdk: true, response: r, failure: e})
	}); err != nil {
		return queryloop.ToolPermission{}, err
	}
	winner, err := race.Await(scope)
	if err != nil {
		return queryloop.ToolPermission{}, err
	}
	if !winner.sdk && winner.failure == nil && winner.decision != nil {
		if err = prompt.Cancel(scope); err != nil {
			return queryloop.ToolPermission{}, err
		}
		return *winner.decision, nil
	}
	if !winner.sdk && winner.failure == nil {
		winner.response, winner.failure = prompt.RawFuture().Await(scope)
	}
	return prompt.Complete(scope, winner.response, winner.failure)
}

// RetainNativePermissionHookSignal is the parent Owner lifecycle seam used by
// the existing independent-child bridge. It exposes only cancellation retention
// and never transfers a handler, Scope or Hook result between Owners.
func (h *Host) RetainNativePermissionHookSignal(scope *cooperative.Scope, cancel context.CancelFunc) (bool, error) {
	if h == nil || !scope.BelongsTo(h.options.Owner) {
		return false, cooperative.ErrRestricted
	}
	return h.nativePermissionHooks.RetainTurnCancel(scope, cancel), nil
}
func (h *Host) retainNativePermissionHookSignal(scope *cooperative.Scope, cancel context.CancelFunc) (bool, error) {
	retained, err := h.RetainNativePermissionHookSignal(scope, cancel)
	if err != nil || retained || h.options.NativePermissionHookRetention == nil {
		return retained, err
	}
	return h.options.NativePermissionHookRetention(scope, cancel)
}
