package application

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (d *DesktopSession) CollaborationRuntime() protocol.JSONObject {
	d.mu.Lock()
	defer d.mu.Unlock()
	state := "idle"
	if d.running {
		state = "running"
	}
	if len(d.engine.PendingPermissions()) > 0 {
		state = "blocked"
	}
	if d.projection.Stopped {
		state = "stopped"
	}
	providerID := raw(nil)
	if d.options.RuntimeSelection != nil {
		providerID = raw(d.options.RuntimeSelection.ProviderID)
	}
	mode := d.options.Arguments.PermissionMode
	if mode == "" {
		mode = "default"
	}
	return protocol.NewJSONObject(field("state", state), field("permissionMode", mode), protocol.JSONField{Name: "providerId", Value: providerID}, field("model", d.engine.process.SelectedModel()), field("effort", d.options.Arguments.EffortLevel))
}

func (d *DesktopSession) EnqueueCollaboration(ctx context.Context, message protocol.JSONObject, startIfIdle bool, canSend func() bool) (protocol.JSONObject, error) {
	d.mu.Lock()
	epoch := d.collaborationStopEpoch
	d.mu.Unlock()
	input := protocol.NewJSONObject(field("subtype", "enqueue_session_message"), protocol.JSONField{Name: "message_id", Value: message.Get("id")}, protocol.JSONField{Name: "sender_session_id", Value: message.Get("sourceSessionId")}, protocol.JSONField{Name: "text", Value: message.Get("content")})
	result, e := d.currentEngine().EnqueueCollaboration(ctx, input, canSend)
	if e != nil || !startIfIdle || result.String("status") == "consumed" {
		return result, e
	}
	if canSend != nil && !canSend() {
		return protocol.JSONObject{}, context.Canceled
	}
	d.runtimeMu.Lock()
	d.mu.Lock()
	if d.closed || d.running || d.collaborationStopEpoch != epoch {
		d.mu.Unlock()
		d.runtimeMu.Unlock()
		return result, nil
	}
	d.running, d.foregroundPending = true, true
	d.projection.Stopped = false
	engine := d.engine
	d.mu.Unlock()
	d.runtimeMu.Unlock()
	d.emit(frame("status", field("state", "thinking"), field("verb", "Thinking")))
	go func() {
		err := engine.RunCollaboration(d.ctx)
		d.mu.Lock()
		d.running, d.foregroundPending = false, false
		d.mu.Unlock()
		if restartErr := d.applyDeferredRuntime(d.ctx); err == nil {
			err = restartErr
		}
		if err != nil {
			d.emit(frame("error", field("code", "USER_TURN_FAILED"), field("message", err.Error())))
		}
	}()
	return result, nil
}

func (s *Session) EnqueueCollaboration(ctx context.Context, input protocol.JSONObject, canSend func() bool) (protocol.JSONObject, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return protocol.JSONObject{}, cooperative.ErrClosed
	}
	return s.process.EnqueueSessionMessage(ctx, input, canSend)
}

func (s *Session) RunCollaboration(ctx context.Context) error {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return cooperative.ErrClosed
	}
	modelContext, cancel := context.WithCancel(ctx)
	s.cancel, s.turnID, s.emit = cancel, "", nil
	s.mu.Unlock()
	defer func() { cancel(); s.mu.Lock(); s.cancel = nil; s.emit = nil; s.mu.Unlock() }()
	return s.process.RunSessionMessages(modelContext)
}
