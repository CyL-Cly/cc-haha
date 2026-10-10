package queryhooks

import (
	"context"
	"encoding/json"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/prompthook"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func nativeEventHookDisplay(hook protocol.JSONObject) string {
	if hook.String("type") == "callback" {
		return "callback"
	}
	if display := hook.String("statusMessage"); display != "" {
		return display
	}
	if hook.String("type") == "prompt" || hook.String("type") == "agent" {
		return hook.String("prompt")
	}
	if hook.String("type") == "http" {
		return hook.String("url")
	}
	return hook.String("command")
}

func nativeEventHookKey(hook protocol.JSONObject) (string, error) {
	condition := hook.Get("if")
	if condition == nil || string(condition) == "null" {
		condition = commandJSON("")
	}
	kind := hook.String("type")
	parts := []json.RawMessage{}
	if kind == "command" {
		shell := hook.Get("shell")
		if shell == nil || string(shell) == "null" {
			shell = commandJSON("bash")
		}
		parts = append(parts, shell, commandJSON("\x00"), hook.Get("command"))
	} else if kind == "http" {
		parts = append(parts, hook.Get("url"))
	} else {
		parts = append(parts, hook.Get("prompt"))
	}
	parts = append(parts, commandJSON("\x00"), condition)
	joined, err := protocol.JoinJavaScriptValuesJSON(parts, "")
	return kind + "\x00" + string(joined), err
}

func (s *Service) runNativeEventPrompt(ctx context.Context, call Context, hook, input protocol.JSONObject, id string) (prompthook.Result, error) {
	outerTimeout := 10 * time.Minute
	var seconds float64
	_ = json.Unmarshal(hook.Get("timeout"), &seconds)
	if seconds != 0 {
		outerTimeout = time.Duration(seconds * float64(time.Second))
	}
	outer, cancel := context.WithTimeout(ctx, outerTimeout)
	defer cancel()
	// These event wrappers pass toolUseContext but no messages to executeHooks.
	// Only the separate Stop/SubagentStop wrapper supplies conversation history.
	call.History, call.Assistants = nil, nil
	value, failure := s.executeNativePromptHook(outer, call, hook, input, id)
	return value, failure
}

func (s *Service) nativePromptEventCompletion(call Context, hook protocol.JSONObject, value prompthook.Result, duration float64) EventResult {
	fields := protocol.NewJSONObject()
	if value.BlockingErrorRaw != nil || value.BlockingError != "" {
		blocked := value.BlockingErrorRaw
		if blocked == nil {
			blocked = commandJSON(value.BlockingError)
		}
		fields.Set("blockingError", commandJSON(protocol.NewJSONObject(protocol.JSONField{Name: "blockingError", Value: blocked}, protocol.JSONField{Name: "command", Value: hook.Get("prompt")})))
	}
	if value.PreventContinuation {
		fields.Set("preventContinuation", []byte("true"))
		fields.Set("stopReason", value.StopReason)
	}
	var message *queryloop.Message
	if value.Payload != nil {
		payload := value.Payload.Clone()
		if payload.String("type") == "hook_success" || payload.String("type") == "hook_non_blocking_error" {
			payload.Set("command", commandJSON(nativeEventHookDisplay(hook)))
			payload.Set("durationMs", commandJSON(duration))
		}
		current := queryloop.Message{Record: s.options.Commands.Factory.CreateAttachment(commandJSON(payload))}
		message = &current
	}
	return EventResult{Fields: fields, Message: message, Outcome: value.Outcome, Hook: hook.Clone()}
}
