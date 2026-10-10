package queryhooks

import (
	"context"
	"encoding/json"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func (s *Service) sdkCommandMatchers(event string) []commandMatcher {
	rows := []commandMatcher{}
	for _, matcher := range s.sdkCallbackMatchers(event) {
		var hooks []protocol.JSONObject
		if decode := json.Unmarshal(matcher.Get("hooks"), &hooks); decode != nil {
			continue
		}
		for _, hook := range hooks {
			rows = append(rows, commandMatcher{matcher.String("matcher"), hook.Clone()})
		}
	}
	return rows
}

func (s *Service) runNativeSDKEvent(ctx context.Context, call Context, hook, input protocol.JSONObject, toolUseID string) (EventResult, error) {
	if !s.HasNativeCallbackBindings(s.options.Commands.Owner) || call.Scope == nil || !call.Scope.BelongsTo(s.options.Commands.Owner) {
		return EventResult{}, cooperative.ErrRestricted
	}
	duration, err := sdkCallbackTimeout(hook.Get("timeout"), 10*time.Minute)
	if err != nil {
		return EventResult{}, err
	}
	active, cancel := outsideCommandContext(ctx, duration)
	defer cancel()
	// The callback argument is the triggering tool ID, not the command-hook
	// execution UUID used by process diagnostics.
	call.HookID = toolUseID
	value, err := s.options.Callbacks.Run(active, call, hook, input)
	if err != nil {
		return EventResult{}, err
	}
	result := EventResult{Outcome: "success", Hook: hook}
	if string(value.Get("async")) == "true" {
		return result, nil
	}
	parsed, err := InterpretJSONOutput(value, JSONOutputContext{Command: "callback", HookName: call.HookEvent + ":Callback", ToolUseID: toolUseID, Event: call.HookEvent, ExpectedEvent: call.HookEvent})
	if err != nil {
		return result, err
	}
	result.Fields = parsed.Fields
	message := queryloop.Message{Record: s.options.Commands.Factory.CreateAttachment(commandJSON(parsed.Attachment))}
	result.Message = &message
	return result, nil
}
