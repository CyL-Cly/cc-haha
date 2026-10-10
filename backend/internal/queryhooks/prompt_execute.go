package queryhooks

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/prompthook"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

func (s *Service) executeNativePromptHook(ctx context.Context, call Context, hook, input protocol.JSONObject, id string) (prompthook.Result, error) {
	bindings := s.options.Prompts
	if bindings == nil || !s.HasNativePromptBindings(bindings.Owner) || !call.Scope.BelongsTo(bindings.Owner) {
		return prompthook.Result{}, cooperative.ErrRestricted
	}
	jsonInput := string(commandJSON(input))
	processed, err := skillcommands.SubstituteArguments(hook.String("prompt"), &jsonInput, nil)
	if err != nil {
		return prompthook.Result{}, err
	}
	history := []protocol.JSONObject{}
	for _, message := range call.History {
		data, err := message.MarshalJSON()
		if err != nil {
			return prompthook.Result{}, err
		}
		var record protocol.JSONObject
		if err = record.UnmarshalJSON(data); err != nil {
			return prompthook.Result{}, err
		}
		history = append(history, record)
	}
	for _, assistant := range call.Assistants {
		data, err := assistant.MarshalJSON()
		if err != nil {
			return prompthook.Result{}, err
		}
		var record protocol.JSONObject
		if err = record.UnmarshalJSON(data); err != nil {
			return prompthook.Result{}, err
		}
		history = append(history, record)
	}
	timeout := 30 * time.Second
	var seconds float64
	_ = json.Unmarshal(hook.Get("timeout"), &seconds)
	if seconds != 0 {
		timeout = time.Duration(seconds * float64(time.Second))
	}
	modelContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	response, failure := bindings.Query(modelContext, call.Scope, PromptModelInput{ToolNames: call.PromptToolNames, Hook: hook.Clone(), History: history, Prompt: commandJSON(processed)})
	var content []protocol.JSONObject
	_ = json.Unmarshal(response.Get("content"), &content)
	parts := []string{}
	for _, block := range content {
		if block.String("type") == "text" {
			units, err := protocol.DecodeJavaScriptJSONString(block.Get("text"))
			if err == nil {
				parts = append(parts, string(utf16.Decode(units)))
			}
		}
	}
	return prompthook.Interpret(hook.String("prompt"), call.HookName, call.HookEvent, id, strings.Join(parts, ""), failure, ctx.Err() != nil, modelContext.Err() != nil && ctx.Err() == nil), nil
}
