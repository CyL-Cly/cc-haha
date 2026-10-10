package application

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/autoquestion"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (d *DesktopSession) bindAutoQuestions() error {
	d.mu.Lock()
	options := d.options
	engine := d.engine
	d.mu.Unlock()
	if options.AutoQuestion == nil {
		return nil
	}
	var providerID *string
	if options.RuntimeSelection != nil {
		providerID = options.RuntimeSelection.ProviderID
	}
	var fingerprint json.RawMessage
	if providerID != nil && options.AutoQuestionProvider != nil {
		var e error
		fingerprint, e = options.AutoQuestionProvider(d.ctx, *providerID)
		if e != nil {
			return e
		}
	}
	d.autoQuestionUnbind = options.AutoQuestion.Bind(d.id, autoquestion.Binding{ProviderID: providerID, ProviderFingerprint: fingerprint, ReadProviderFingerprint: options.AutoQuestionProvider, UsesOfficialOAuth: engine.native.Environment["CLAUDE_CODE_OAUTH_TOKEN"] != "" && engine.native.Environment["CLAUDE_CODE_ENTRYPOINT"] == "claude-desktop", ReadContext: func(ctx context.Context, agentID string) (json.RawMessage, error) {
		if options.AutoQuestionContext == nil {
			return raw(""), nil
		}
		return options.AutoQuestionContext(ctx, d.id, agentID)
	}, Resolve: d.resolveAutomaticQuestion})
	return nil
}
func (d *DesktopSession) automaticQuestions() *autoquestion.Service {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.options.AutoQuestion
}
func (d *DesktopSession) trackAutoQuestion(id, tool, agent string, input json.RawMessage) {
	if service := d.automaticQuestions(); service != nil {
		service.Track(d.id, autoquestion.Request{ID: id, ToolName: tool, AgentID: protocol.TrimJavaScriptString(agent), Input: input})
	}
}
func (d *DesktopSession) removeAutoQuestion(id string) {
	if service := d.automaticQuestions(); service != nil {
		service.Remove(d.id, id)
	}
}
func (d *DesktopSession) resolveAutomaticQuestion(ctx context.Context, id string, input json.RawMessage) (bool, error) {
	// Only this private callback receives validated model choices from the timed
	// service. Public UI/model paths continue stripping the automatic marker.
	if context.Cause(ctx) != nil {
		return false, context.Cause(ctx)
	}
	state := d.sdkRuntime()
	state.mu.Lock()
	request, foreign := state.pending[id]
	if foreign && request.String("tool_name") == "AskUserQuestion" && !state.closed && !state.stopped {
		state.removePending(id)
		state.autoResolved[id] = true
	} else {
		foreign = false
	}
	state.mu.Unlock()
	if foreign {
		decision := protocol.NewJSONObject(field("behavior", "allow"), protocol.JSONField{Name: "updatedInput", Value: input})
		response := frame("control_response", field("response", protocol.NewJSONObject(field("subtype", "success"), field("request_id", id), protocol.JSONField{Name: "response", Value: raw(decision)})))
		if e := d.sendSDK(response); e != nil {
			return false, e
		}
		d.emitAutomaticQuestionResolution(id)
		return true, nil
	}
	engine := d.currentEngine()
	if engine == nil {
		return false, nil
	}
	found := false
	for _, p := range engine.PendingPermissions() {
		if p.ID == id && p.ToolName == "AskUserQuestion" {
			found = true
			break
		}
	}
	if !found {
		return false, nil
	}
	if e := engine.RespondPermission(ctx, id, tui.PermissionDecision{Allowed: true, UpdatedInput: input}); e != nil {
		return false, e
	}
	state.mu.Lock()
	state.autoResolved[id] = true
	state.mu.Unlock()
	d.notifyAutomaticQuestionSDK(id)
	return true, nil
}
func (d *DesktopSession) emitAutomaticQuestionResolution(id string) {
	d.notifyAutomaticQuestionSDK(id)
	d.emit(frame("permission_resolved", field("requestId", id), field("permissionType", "tool"), field("allowed", true)))
}
func (d *DesktopSession) notifyAutomaticQuestionSDK(id string) {
	if d.onSDK != nil {
		d.onSDK(frame("control_response", field("response", protocol.NewJSONObject(field("request_id", id), field("response", map[string]string{"behavior": "allow"})))))
	}
}
func (d *DesktopSession) cancelAutoQuestion(id string) {
	if service := d.automaticQuestions(); service != nil {
		service.CancelActivity(d.id, id)
	}
}
