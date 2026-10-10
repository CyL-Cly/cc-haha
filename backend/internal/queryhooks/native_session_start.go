package queryhooks

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type NativeSessionStartInput struct {
	SessionID, Source string
	AgentType, Model  json.RawMessage
	Call              Context
	Base              protocol.JSONObject
}

type SessionStartResult struct {
	Messages           []protocol.JSONObject
	InitialUserMessage json.RawMessage
	WatchPaths         []json.RawMessage
}

// ExecuteSessionStart consumes the original wrapper and processSessionStartHooks
// stages. Hook blocking/continuation decisions do not replace startup history.
func (s *Service) ExecuteSessionStart(ctx context.Context, in NativeSessionStartInput) (SessionStartResult, error) {
	result := SessionStartResult{Messages: []protocol.JSONObject{}, WatchPaths: []json.RawMessage{}}
	if s == nil || s.options.Commands == nil || in.Call.Scope == nil || !in.Call.Scope.BelongsTo(s.options.Commands.Owner) {
		return result, cooperative.ErrRestricted
	}
	s.mu.Lock()
	simple := s.simple()
	s.mu.Unlock()
	if simple {
		return result, nil
	}
	if in.SessionID == "" {
		return result, errors.New("SessionStart requires actual session identity")
	}
	switch in.Source {
	case "startup", "resume", "clear", "compact":
	default:
		return result, unsupported("SessionStart source " + in.Source)
	}
	fields := protocol.NewJSONObject(protocol.JSONField{Name: "session_id", Value: commandJSON(in.SessionID)}, protocol.JSONField{Name: "permission_mode", Value: nil}, protocol.JSONField{Name: "agent_id", Value: nil}, protocol.JSONField{Name: "source", Value: commandJSON(in.Source)}, protocol.JSONField{Name: "agent_type", Value: in.AgentType}, protocol.JSONField{Name: "model", Value: in.Model})
	for _, field := range in.Base.OwnProperties() {
		_ = fields.SetPropertyJSON(field.NameJSON, field.Value)
	}
	id := s.options.UUID()
	contexts := []json.RawMessage{}
	err := s.ExecuteCommandEvent(ctx, CommandEvent{Event: "SessionStart", SessionID: in.SessionID, ToolUseID: id, MatchQuery: in.Source, Fields: fields, Call: in.Call}, func(value EventResult) error {
		return s.expandNativeTaskResult("SessionStart", id, value, func(frame EventResult) error {
			if frame.Message != nil {
				raw, err := frame.Message.MarshalJSON()
				if err != nil {
					return err
				}
				var message protocol.JSONObject
				if err = message.UnmarshalJSON(raw); err != nil {
					return err
				}
				result.Messages = append(result.Messages, message)
			}
			var extra []json.RawMessage
			if raw := frame.Fields.Get("additionalContexts"); raw != nil {
				if err := json.Unmarshal(raw, &extra); err != nil {
					return err
				}
				contexts = append(contexts, extra...)
			}
			if raw := frame.Fields.Get("initialUserMessage"); hookTruthy(raw) {
				result.InitialUserMessage = raw
			}
			if raw := frame.Fields.Get("watchPaths"); raw != nil {
				if err := json.Unmarshal(raw, &extra); err != nil {
					return err
				}
				result.WatchPaths = append(result.WatchPaths, extra...)
			}
			return nil
		}, "SessionStart:"+in.Source)
	})
	if err != nil {
		return result, err
	}
	if len(contexts) > 0 {
		attachment := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: commandJSON("hook_additional_context")}, protocol.JSONField{Name: "content", Value: commandJSON(contexts)}, protocol.JSONField{Name: "hookName", Value: commandJSON("SessionStart")}, protocol.JSONField{Name: "toolUseID", Value: commandJSON("SessionStart")}, protocol.JSONField{Name: "hookEvent", Value: commandJSON("SessionStart")})
		result.Messages = append(result.Messages, s.options.Commands.Factory.CreateAttachment(commandJSON(attachment)))
	}
	return result, nil
}
