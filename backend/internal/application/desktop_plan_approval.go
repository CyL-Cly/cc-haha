package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// approvePlanRuntime is the original approval consumer, not a general runtime
// switch. Its set_model acknowledgement and persistence must precede release
// of this very ExitPlanMode permission on the same live engine/Owner.
func (d *DesktopSession) approvePlanRuntime(ctx context.Context, message protocol.JSONObject, expected *Session) error {
	d.runtimeMu.Lock()
	locked := true
	defer func() {
		if locked {
			d.runtimeMu.Unlock()
		}
	}()
	d.mu.Lock()
	closed, current := d.closed, d.engine
	accepted := d.acceptedPlanApprovals[message.String("requestId")] == expected
	accepted = accepted || d.planRestart != nil && d.planRestart.engine == expected && d.planRestart.requestID == message.String("requestId")
	d.mu.Unlock()
	if closed || current != expected {
		return context.Canceled
	}
	if accepted {
		return nil
	}
	options := d.options.ProviderOptions
	if options == nil {
		store := d.options.Store
		if store == nil {
			store = storage.New(storage.Options{})
		}
		options = &providerconfig.Options{Store: store, ConfigDir: d.options.Environment["CLAUDE_CONFIG_DIR"]}
	}
	var input storage.Object
	if e := input.UnmarshalJSON(message.Get("runtimeOverride")); e != nil {
		d.emit(frame("error", field("code", "RUNTIME_CONFIG_INVALID"), field("message", "Runtime model selection is invalid.")))
		return nil
	}
	provider, effort := d.currentRuntimeIdentity()
	// Source rejects a started side-chat provider change before its deleted-
	// provider fallback can consult or select another saved provider.
	var requestedProvider *string
	if value := input["providerId"]; len(value) > 0 && string(value) != "null" {
		var id string
		if json.Unmarshal(value, &id) == nil {
			requestedProvider = &id
		}
	}
	if d.sideChat && !sameRuntimeProvider(provider, requestedProvider) {
		d.rejectSideChatRestart("Open a new side chat to change provider or reasoning effort.")
		return nil
	}
	next, e := providerconfig.NormalizeRuntimeSelection(ctx, *options, input)
	if e != nil {
		var api *settings.APIError
		if errors.As(e, &api) && api.Code == "RUNTIME_CONFIG_INVALID" {
			d.emit(frame("error", field("code", api.Code), field("message", api.Message)))
			return nil
		}
		return d.planRuntimeFailure(e)
	}
	d.mu.Lock()
	model := d.options.Arguments.Model
	if selected := d.options.RuntimeSelection; selected != nil {
		model = selected.ModelID
	}
	d.mu.Unlock()
	if sameRuntimeProvider(provider, next.ProviderID) && model == next.ModelID && effort == next.EffortLevel {
		d.mu.Lock()
		d.rejectedRuntime = ""
		d.mu.Unlock()
		return d.finishOwnPlanApproval(ctx, message, expected)
	}
	canSwitch := sameRuntimeProvider(provider, next.ProviderID) && (next.EffortLevel == "" || next.EffortLevel == effort)
	if !canSwitch {
		if d.sideChat {
			d.rejectSideChatRestart("Open a new side chat to change provider or reasoning effort.")
			return nil
		}
		return d.restartApprovedPlan(ctx, message, expected, next, &locked)
	}
	if e = expected.process.SetSessionModel(ctx, next.ModelID); e != nil {
		return d.planRuntimeFailure(e)
	}
	if next.EffortLevel == "" && effort != "" {
		next.EffortLevel = effort
		if next.RequestedConfig == nil {
			next.RequestedConfig = input
		}
	}
	// Original runtimeOverrides is committed after the ACK and before the
	// persistence await. A failed persistence still leaves the permission
	// pending, while a retry of that exact selection follows the same-value
	// branch, rather than sending an additional set_model control.
	d.mu.Lock()
	d.rejectedRuntime = ""
	d.options.Arguments.Model = next.ModelID
	d.options.Arguments.EffortLevel = next.EffortLevel
	d.options.RuntimeSelection = &next.Selection
	d.mu.Unlock()
	if e = d.persistRuntimeSelection(ctx, expected, next); e != nil {
		return d.planRuntimeFailure(e)
	}
	d.broadcastAppliedRuntime(next)
	return d.finishOwnPlanApproval(ctx, message, expected)
}

func (d *DesktopSession) finishOwnPlanApproval(ctx context.Context, message protocol.JSONObject, expected *Session) error {
	decision, e := desktopPermissionDecision(message, "ExitPlanMode")
	if e != nil {
		return e
	}
	decision.PermissionUpdates = message.Get("permissionUpdates")
	if e = expected.RespondPermission(ctx, message.String("requestId"), decision); e != nil {
		return e
	}
	d.mu.Lock()
	if d.acceptedPlanApprovals == nil {
		d.acceptedPlanApprovals = map[string]*Session{}
	}
	d.acceptedPlanApprovals[message.String("requestId")] = expected
	d.mu.Unlock()
	return nil
}

func (d *DesktopSession) IsPlanRuntimeApproval(data json.RawMessage) bool {
	message := obj(data)
	if message.String("type") != "permission_response" || string(message.Get("allowed")) != "true" || !sourceSDKTruthy(message.Get("runtimeOverride")) {
		return false
	}
	for _, pending := range d.currentEngine().PendingPermissions() {
		if pending.ID == message.String("requestId") && pending.ToolName == "ExitPlanMode" {
			return true
		}
	}
	return false
}

func (d *DesktopSession) planRuntimeFailure(failure error) error {
	d.mu.Lock()
	stderr := d.options.Stderr
	d.mu.Unlock()
	if stderr != nil {
		_, _ = fmt.Fprintf(stderr, "[WS] Plan approval with runtime override failed for %s: %v\n", d.id, failure)
	}
	d.emit(frame("error", field("code", "RUNTIME_CONFIG_INVALID"), field("message", "Failed to apply the execution model. The plan is still waiting for approval.")))
	return nil
}

// The approved old ASK keeps its captured model. Only the new same-SID graph
// reads the newly selected provider environment and receives the original
// synthetic continue message after the old result/close boundary.
type desktopPlanRestart struct {
	requestID string
	engine    *Session
	result    chan struct{}
	returned  chan struct{}
	finished  chan struct{}
	continued bool
}

type planContinueProducerKey struct{}
type planPendingInputKey struct{}

func (d *DesktopSession) CaptureRuntimeInput(ctx context.Context) context.Context {
	d.mu.Lock()
	restart := d.planRestart
	d.mu.Unlock()
	if restart != nil {
		return context.WithValue(ctx, planPendingInputKey{}, restart)
	}
	return ctx
}

func (d *DesktopSession) restartApprovedPlan(ctx context.Context, message protocol.JSONObject, expected *Session, next providerconfig.RuntimeSelection, locked *bool) error {
	d.mu.Lock()
	d.rejectedRuntime = ""
	d.options.Arguments.Model = next.ModelID
	d.options.Arguments.EffortLevel = next.EffortLevel
	d.options.RuntimeSelection = &next.Selection
	d.mu.Unlock()
	if err := d.persistRuntimeSelection(ctx, expected, next); err != nil {
		return d.planRuntimeFailure(err)
	}
	var updates []protocol.JSONObject
	if json.Unmarshal(message.Get("permissionUpdates"), &updates) == nil {
		for _, update := range updates {
			mode := update.String("mode")
			if update.String("type") != "setMode" || update.String("destination") != "session" {
				continue
			}
			switch mode {
			case "default", "acceptEdits", "bypassPermissions", "plan", "auto", "dontAsk":
			default:
				continue
			}
			metadata := protocol.NewJSONObject(field("permissionMode", mode))
			if err := expected.process.AppendSessionMetadata(ctx, metadata); err != nil {
				return d.planRuntimeFailure(err)
			}
			if d.updateLaunchMetadata != nil {
				if err := d.updateLaunchMetadata(ctx, payloadJSON(metadata)); err != nil {
					return d.planRuntimeFailure(err)
				}
			}
			d.mu.Lock()
			d.options.Arguments.PermissionMode = mode
			d.mu.Unlock()
			break
		}
	}
	transition := &desktopPlanRestart{requestID: message.String("requestId"), engine: expected, result: make(chan struct{}), returned: make(chan struct{}), finished: make(chan struct{})}
	d.mu.Lock()
	d.planRestart = transition
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		if d.planRestart == transition {
			d.planRestart = nil
		}
		close(transition.finished)
		d.mu.Unlock()
	}()
	// Release the runtime transition while the original permission and stop
	// consumers drain; Close and other controls keep their existing lifetime.
	d.runtimeMu.Unlock()
	*locked = false
	if err := d.finishOwnPlanApproval(ctx, message, expected); err != nil {
		return d.planRuntimeFailure(err)
	}
	if err := d.Handle(ctx, frame("stop_generation")); err != nil {
		return d.planRuntimeFailure(err)
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-transition.result:
	case <-timer.C:
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	d.runtimeMu.Lock()
	*locked = true
	d.mu.Lock()
	current, closed := d.engine, d.closed
	d.mu.Unlock()
	if closed || current != expected {
		return context.Canceled
	}
	if err := d.restartRuntime(ctx, next); err != nil {
		d.mu.Lock()
		stderr := d.options.Stderr
		reported := d.rejectedRuntime != ""
		d.mu.Unlock()
		if stderr != nil {
			_, _ = fmt.Fprintf(stderr, "[WS] Failed to restart approved plan for %s: %v\n", d.id, err)
		}
		if !reported {
			d.emit(frame("error", field("code", "CLI_RESTART_FAILED"), field("message", "Failed to switch provider/model: "+err.Error())))
		}
		d.emit(frame("status", field("state", "idle")))
		return nil
	}
	d.runtimeMu.Unlock()
	*locked = false
	if d.canContinueRuntime == nil || !d.canContinueRuntime() {
		return nil
	}
	d.mu.Lock()
	transition.continued = true
	d.mu.Unlock()
	go func() {
		ctx := context.WithValue(d.ctx, planContinueProducerKey{}, transition)
		if err := d.Handle(ctx, frame("user_message", field("content", "The plan is approved. Continue with the implementation."))); err != nil {
			d.mu.Lock()
			stderr := d.options.Stderr
			d.mu.Unlock()
			if stderr != nil {
				_, _ = fmt.Fprintf(stderr, "[WS] Failed to auto-continue plan execution for %s: %v\n", d.id, err)
			}
		}
	}()
	return nil
}
