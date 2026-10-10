package server

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/httpapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func collaborationUnhandled(e error) httpapi.UnhandledError { return httpapi.UnhandledError{Error: e} }

func (s *Server) observeCollaborationSDK(id string, data json.RawMessage) {
	var message protocol.JSONObject
	if message.UnmarshalJSON(data) != nil {
		return
	}
	if message.String("type") != "result" && message.String("type") != "control_request" && message.String("type") != "control_response" && message.String("type") != "control_cancel_request" && !(message.String("type") == "system" && (message.String("subtype") == "session_message_receipt" || message.String("subtype") == "init")) {
		return
	}
	h := s.collaboration
	h.mu.Lock()
	h.events = append(h.events, collaborationEvent{id: id, data: append(json.RawMessage(nil), data...)})
	h.mu.Unlock()
	select {
	case h.wake <- struct{}{}:
	default:
	}
}
func (s *Server) collaborationEvents(h *collaborationHost) {
	defer close(h.done)
	for {
		h.mu.Lock()
		var event collaborationEvent
		if len(h.events) > 0 {
			event = h.events[0]
			h.events[0] = collaborationEvent{}
			h.events = h.events[1:]
		}
		h.mu.Unlock()
		if event.id == "" {
			select {
			case <-s.ctx.Done():
				return
			case <-h.wake:
				continue
			}
		}
		if s.ctx.Err() != nil {
			return
		}
		if event.barrier != nil {
			close(event.barrier)
			continue
		}
		if h.failure != nil {
			continue
		}
		message := collaborationObject(json.RawMessage(event.data))
		changed, e := s.handleCollaborationSDK(s.ctx, event.id, message)
		if e != nil {
			s.options.OnUnhandled(collaborationUnhandled(e))
			continue
		}
		if changed {
			s.broadcast(event.id, json.RawMessage(`{"type":"system_notification","subtype":"session_collaboration_updated","data":{"sessionId":`+string(quotedCollaboration(event.id))+`}}`))
		}
	}
}
func (s *Server) collaborationBarrier(ctx context.Context) error {
	if _, e := s.collaborationService(ctx); e != nil {
		return e
	}
	wake := make(chan struct{})
	h := s.collaboration
	h.mu.Lock()
	h.events = append(h.events, collaborationEvent{id: "barrier", barrier: wake})
	h.mu.Unlock()
	select {
	case h.wake <- struct{}{}:
	default:
	}
	select {
	case <-wake:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-s.ctx.Done():
		return context.Cause(s.ctx)
	}
}
func (s *Server) handleCollaborationSDK(ctx context.Context, id string, message protocol.JSONObject) (bool, error) {
	service := s.collaboration.service
	switch message.String("type") {
	case "system":
		if message.String("subtype") == "session_message_receipt" && message.String("status") == "consumed" {
			return true, service.OnMessageConsumed(ctx, message.String("message_id"), id)
		}
		if message.String("subtype") == "init" {
			return false, service.OnSessionState(ctx, id, "running", protocol.NewJSONObject(), "")
		}
	case "result":
		state := "completed"
		if string(message.Get("is_error")) == "true" {
			state = "failed"
		}
		result := message.Get("result")
		if result == nil {
			var values []string
			_ = json.Unmarshal(message.Get("errors"), &values)
			text := ""
			for i, value := range values {
				if i > 0 {
					text += "\n"
				}
				text += value
			}
			result = quotedCollaboration(text)
		}
		result, e := protocol.ExactJavaScriptStringJSON(result)
		if e != nil {
			return false, e
		}
		units, _ := protocol.DecodeJavaScriptJSONString(result)
		result = protocol.QuoteJavaScriptUTF16(units[:min(8000, len(units))])
		return true, service.OnSessionState(ctx, id, state, protocol.NewJSONObject(protocol.JSONField{Name: "result", Value: result}), message.String("uuid"))
	case "control_request":
		var request protocol.JSONObject
		_ = request.UnmarshalJSON(message.Get("request"))
		if request.String("subtype") == "can_use_tool" {
			name := request.String("tool_name")
			if name == "" {
				name = "tool"
			}
			return true, service.OnSessionState(ctx, id, "blocked", collaborationObject(map[string]any{"result": "Waiting for permission: " + name}), message.String("request_id"))
		}
	case "control_response", "control_cancel_request":
		if s.collaborationRuntimeState(id) == "running" {
			return true, service.OnSessionState(ctx, id, "running", protocol.NewJSONObject(), "")
		}
	}
	return false, nil
}
