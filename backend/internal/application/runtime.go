package application

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func (d *DesktopSession) setRuntime(ctx context.Context, input protocol.JSONObject) error {
	d.runtimeMu.Lock()
	defer d.runtimeMu.Unlock()
	d.mu.Lock()
	closed := d.closed
	d.mu.Unlock()
	if closed {
		return context.Canceled
	}
	options := d.options.ProviderOptions
	if options == nil {
		store := d.options.Store
		if store == nil {
			store = storage.New(storage.Options{})
		}
		options = &providerconfig.Options{Store: store, ConfigDir: d.options.Environment["CLAUDE_CONFIG_DIR"]}
	}
	var values storage.Object
	if err := values.UnmarshalJSON(payloadJSON(input)); err != nil {
		return err
	}
	currentProvider, currentEffort := d.currentRuntimeIdentity()
	var requestedProvider *string
	if value := values["providerId"]; len(value) > 0 && string(value) != "null" {
		var id string
		if err := json.Unmarshal(value, &id); err == nil {
			requestedProvider = &id
		}
	}
	if d.sideChat && !sameRuntimeProvider(currentProvider, requestedProvider) {
		d.rejectSideChatRestart("Open a new side chat to change provider or reasoning effort.")
		return nil
	}
	selection, err := providerconfig.NormalizeRuntimeSelection(ctx, *options, values)
	if err != nil {
		var api *settings.APIError
		if errors.As(err, &api) && api.Code == "RUNTIME_CONFIG_INVALID" {
			d.mu.Lock()
			d.rejectedRuntime = api.Message
			d.mu.Unlock()
			d.emit(frame("error", field("code", api.Code), field("message", api.Message)))
			return nil
		}
		return err
	}
	if d.acceptReplayedRuntime(selection) {
		return nil
	}
	canSwitchInProcess := d.currentOpenAIFastMode() == selectionFast(selection.Selection) && sameRuntimeProvider(currentProvider, selection.ProviderID) && (selection.EffortLevel == "" || selection.EffortLevel == currentEffort)
	if d.sideChat && !canSwitchInProcess {
		d.rejectSideChatRestart("A temporary side chat cannot restart without losing its history. Open a new side chat to change provider or reasoning effort.")
		return nil
	}
	// Source set_runtime_config restarts durable chats. Only its explicitly
	// ephemeral branch uses SDK set_model to retain temporary history.
	inProcess := d.sideChat && canSwitchInProcess
	if inProcess {
		if err := d.currentEngine().process.SetSessionModel(ctx, selection.ModelID); err != nil {
			return err
		}
		if selection.EffortLevel == "" {
			selection.EffortLevel = currentEffort
		}
	}
	if err = d.persistRuntimeSelection(ctx, d.currentEngine(), selection); err != nil {
		return err
	}
	d.mu.Lock()
	d.rejectedRuntime = ""
	if inProcess {
		d.options.Arguments.Model = selection.ModelID
		d.options.Arguments.EffortLevel = selection.EffortLevel
		d.options.RuntimeSelection = &selection.Selection
		d.mu.Unlock()
		d.broadcastAppliedRuntime(selection)
		return nil
	}
	running := d.running
	if running {
		d.deferred = &selection
		d.mu.Unlock()
		return nil
	}
	d.mu.Unlock()
	return d.restartRuntime(ctx, selection)
}

func (d *DesktopSession) persistRuntimeSelection(ctx context.Context, engine *Session, selection providerconfig.RuntimeSelection) error {
	metadata := protocol.NewJSONObject(protocol.JSONField{Name: "runtimeProviderId", Value: raw(selection.ProviderID)}, field("runtimeModelId", selection.ModelID))
	if selection.EffortLevel != "" {
		metadata.Set("effortLevel", raw(selection.EffortLevel))
	}
	metadata.Set("openAIFastMode", raw(selectionFast(selection.Selection)))
	// AppendSessionMetadata enriches the shared record for transcript storage.
	// The original host metadata callback received the earlier runtime-only
	// bytes, captured before that append.
	launchMetadata := payloadJSON(metadata)
	if err := engine.process.AppendSessionMetadata(ctx, metadata); err != nil {
		return err
	}
	if d.updateLaunchMetadata != nil {
		return d.updateLaunchMetadata(ctx, launchMetadata)
	}
	return nil
}

func (d *DesktopSession) applyDeferredRuntime(ctx context.Context) error {
	d.runtimeMu.Lock()
	defer d.runtimeMu.Unlock()
	d.mu.Lock()
	next := d.deferred
	d.deferred = nil
	closed := d.closed
	d.mu.Unlock()
	if closed || next == nil {
		return nil
	}
	return d.restartRuntime(ctx, *next)
}

// Source changes provider/effort by restarting the runtime after the admitted
// turn, restoring the original transcript into the same shared Go graph.
func (d *DesktopSession) restartRuntime(ctx context.Context, next providerconfig.RuntimeSelection) error {
	previous := d.currentEngine()
	d.mu.Lock()
	interrupted := d.planRestart
	d.mu.Unlock()
	if interrupted != nil && interrupted.engine == previous {
		// A displayed result precedes the existing ASK's checkpoint/finally.
		// Source kills that old process before reload; native restart must join
		// its actual submit return before issuing any new Owner command.
		select {
		case <-interrupted.returned:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	hasConversation, err := previous.hasConversation(ctx)
	if err != nil {
		return err
	}
	o := d.options
	o.RuntimeSelection = &next.Selection
	o.Environment, err = desktopTraceEnvironment(ctx, o)
	if err != nil {
		return err
	}
	finishTransition := d.beginRuntimeTransition()
	defer finishTransition()
	output := d.nextRuntimeOutput()
	if err := previous.Close(ctx); err != nil {
		return err
	}
	o.Native = nil
	o.Arguments.SessionID = d.id
	o.Arguments.Resume = ""
	if hasConversation {
		o.Arguments.SessionID = ""
		o.Arguments.Resume = d.id
	}
	o.Arguments.Model = next.ModelID
	o.Arguments.EffortLevel = next.EffortLevel
	o.RuntimeSelection = &next.Selection
	o.OnSDK = output
	engine, err := NewSession(d.ctx, o)
	if err != nil {
		// Restore the previous frozen native environment so a failed restart
		// leaves a usable runtime, even if the active saved provider changed.
		rollback := d.options
		rollback.Native = previous.native
		rollback.OnSDK = d.nextRuntimeOutput()
		rollback.Arguments.SessionID = d.id
		rollback.Arguments.Resume = ""
		if hasConversation {
			rollback.Arguments.SessionID = ""
			rollback.Arguments.Resume = d.id
		}
		recovered, recoveryErr := NewSession(d.ctx, rollback)
		d.mu.Lock()
		if recoveryErr == nil {
			d.engine = recovered
		}
		d.rejectedRuntime = "Failed to switch provider/model: " + err.Error()
		d.mu.Unlock()
		d.emit(frame("error", field("code", "CLI_RESTART_FAILED"), field("message", "Failed to switch provider/model: "+err.Error())))
		if recoveryErr != nil {
			return errors.Join(err, recoveryErr)
		}
		return err
	}
	d.mu.Lock()
	d.engine = engine
	d.options = o
	d.mu.Unlock()
	if err := d.bindAutoQuestions(); err != nil {
		return err
	}
	d.broadcastAppliedRuntime(next)
	d.emit(frame("status", field("state", "idle")))
	return nil
}

func (d *DesktopSession) broadcastAppliedRuntime(next providerconfig.RuntimeSelection) {
	fields := []protocol.JSONField{field("providerId", next.ProviderID), field("modelId", next.ModelID)}
	if next.EffortLevel != "" {
		fields = append(fields, field("effortLevel", next.EffortLevel))
	}
	if next.ProviderID != nil && *next.ProviderID == "openai-official" {
		fields = append(fields, field("openAIFastMode", selectionFast(next.Selection)))
	}
	if next.RequestedConfig != nil {
		fields = append(fields, field("requestedConfig", next.RequestedConfig))
	}
	d.emit(frame("runtime_config_applied", fields...))
}

func sameRuntimeProvider(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}
func (d *DesktopSession) currentRuntimeIdentity() (*string, string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if selected := d.options.RuntimeSelection; selected != nil {
		return selected.ProviderID, selected.EffortLevel
	}
	return nil, d.options.Arguments.EffortLevel
}
func (d *DesktopSession) rejectSideChatRestart(message string) {
	d.emit(frame("error", field("code", "SIDE_CHAT_RUNTIME_RESTART_UNAVAILABLE"), field("message", message)))
}

func selectionFast(selection providerconfig.Selection) bool {
	return selection.ProviderID != nil && *selection.ProviderID == "openai-official" && selection.OpenAIFastMode != nil && *selection.OpenAIFastMode
}
func (d *DesktopSession) currentOpenAIFastMode() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.options.RuntimeSelection != nil && selectionFast(*d.options.RuntimeSelection)
}
