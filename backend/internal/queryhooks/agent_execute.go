package queryhooks

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/agenthook"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/prompthook"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"time"
)

func (s *Service) HasNativeAgentBindings(owner *cooperative.Owner) bool {
	return s != nil && s.options.Agents != nil && s.options.Agents.Owner == owner && s.options.Agents.Query != nil && s.HasNativeCommandBindings(owner)
}

func (s *Service) executeNativeAgentHook(ctx context.Context, call Context, hook, input protocol.JSONObject, id string) (prompthook.Result, error) {
	binding := s.options.Agents
	if binding == nil || !s.HasNativeAgentBindings(binding.Owner) || call.Scope == nil || !call.Scope.BelongsTo(binding.Owner) {
		return prompthook.Result{}, cooperative.ErrRestricted
	}
	if call.ToolUseContext == nil {
		return prompthook.Result{}, errors.New("ToolUseContext is required for agent hooks. This is a bug.")
	}
	if id == "" {
		id = "hook-" + s.options.UUID()
	}
	transcript, err := binding.Transcript(call.Scope, call)
	if err != nil {
		return agenthook.Interpret(hook.String("prompt"), call.HookName, call.HookEvent, id, nil, err, false, false), nil
	}
	jsonInput := string(commandJSON(input))
	processed, err := skillcommands.SubstituteArguments(hook.String("prompt"), &jsonInput, nil)
	if err != nil {
		return agenthook.Interpret(hook.String("prompt"), call.HookName, call.HookEvent, id, nil, err, false, false), nil
	}
	user := messages.OrderedUser(s.options.Commands.Factory.CreateUser(storage.Object{"content": commandJSON(processed)}))
	model, err := binding.Model(call.Scope, hook)
	if err != nil {
		return agenthook.Interpret(hook.String("prompt"), call.HookName, call.HookEvent, id, nil, err, false, false), nil
	}
	agentID := "hook-agent-" + s.options.UUID()
	timeout := time.Minute
	var seconds float64
	_ = json.Unmarshal(hook.Get("timeout"), &seconds)
	if seconds != 0 {
		timeout = time.Duration(seconds * float64(time.Second))
	}
	active, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var structured *protocol.JSONObject
	turns := 0
	hitMax := false
	failure := binding.Query(active, call.Scope, AgentModelInput{Hook: hook.Clone(), Prompt: user, HookAgentID: agentID, Model: model, SystemPrompt: agenthook.SystemPrompt(transcript), TranscriptPath: transcript, ToolUseContext: call.ToolUseContext}, func(message queryloop.Message) (bool, error) {
		if message.Type() == "assistant" {
			turns++
			if turns >= 50 {
				hitMax = true
				return false, nil
			}
		}
		if message.Type() == "attachment" {
			attachment, _ := message.Record.Object("attachment")
			if attachment.String("type") == "structured_output" {
				if value, ok := agenthook.ParseStructuredResult(attachment.Get("data")); ok {
					structured = value
					return false, nil
				}
			}
		}
		return true, nil
	})
	// Source reaches clearSessionHooks after normal iterator settlement, even
	// when a structured result/max-turn break aborts the hook controller. Its
	// throwing query path leaves the temporary hook until session cleanup.
	if failure == nil {
		if err = binding.Clear(call.Scope, agentID); err != nil {
			return agenthook.Interpret(hook.String("prompt"), call.HookName, call.HookEvent, id, nil, err, active.Err() != nil, hitMax), nil
		}
	}
	if failure != nil {
		var capability *CapabilityError
		if errors.As(failure, &capability) {
			return prompthook.Result{}, failure
		}
	}
	return agenthook.Interpret(hook.String("prompt"), call.HookName, call.HookEvent, id, structured, failure, active.Err() != nil, hitMax), nil
}
