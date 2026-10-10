package sdkhost

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelyield"
	"github.com/NanmiCoder/cc-haha/backend/internal/prompthook"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

// QueryPromptHook consumes the original secondary-model producer through its
// final settlement. Credentials/accounting stay shared; auxiliary input never
// becomes a new main-session message or a second Agent loop.
func (h *Host) QueryPromptHook(ctx context.Context, scope *cooperative.Scope, input queryhooks.PromptModelInput, model string) (protocol.JSONObject, error) {
	if h == nil || !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess || !h.hooks.HasNativePromptBindings(h.options.Owner) || !h.nativePromptHookModels.Owns(h.options.Owner) {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	var result protocol.JSONObject
	err := h.nativePromptHookModels.Run(scope, ctx, func(producer *cooperative.Scope) error {
		var err error
		result, err = h.queryPromptHookOnProducer(ctx, producer, input, model)
		return err
	})
	return result, err
}

func (h *Host) queryPromptHookOnProducer(ctx context.Context, scope *cooperative.Scope, input queryhooks.PromptModelInput, model string) (protocol.JSONObject, error) {
	deviceID, err := h.requestDeviceID(scope)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	history := make([]protocol.JSONObject, 0, len(input.History)+1)
	for _, record := range input.History {
		history = append(history, record.Clone())
	}
	user := messages.OrderedUser(h.options.Factory.CreateUser(storage.Object{"content": input.Prompt}))
	history = append(history, user)
	definitions := append([]toolschema.Definition{}, h.definitions...)
	allowed := map[string]bool{}
	if input.ToolNames != nil {
		for _, name := range *input.ToolNames {
			allowed[name] = true
		}
		definitions = nil
		for _, definition := range h.definitions {
			if allowed[definition.Name] {
				definitions = append(definitions, definition)
			}
		}
	}
	host := requestcontext.Host{Profile: requestcontext.NativeDirectRelayProfile, Env: h.requestEnvironment, DeviceID: deviceID, SessionID: h.options.Controller.SessionID(), NativeToolDefinitions: &definitions, SDKBetas: []string{}, TTLAllowlist: []string{}, ToolSchemas: h.toolSchemas}
	if h.options.Print.Tools != nil {
		host.ToolSchemaHost, err = h.options.Print.Tools.SchemaHostForScope(scope, model, h.options.OriginalEnv["ANTHROPIC_BASE_URL"])
		if err != nil {
			return protocol.JSONObject{}, err
		}
	}
	falseValue := false
	tools := []protocol.JSONObject{}
	for _, tool := range h.originalTools {
		if input.ToolNames == nil || allowed[tool.String("name")] {
			tools = append(tools, tool.Clone())
		}
	}
	request := requestretry.Input{Context: requestcontext.Context{History: history, SystemPrompt: []json.RawMessage{quote(prompthook.SystemPrompt)}, Tools: tools, Thinking: requestcontext.Thinking{Type: "disabled"}, Options: requestcontext.Options{Model: model, QuerySource: "hook_prompt", IsNonInteractiveSession: true, HasAppendSystemPrompt: false, OutputFormat: raw(map[string]any{"type": "json_schema", "schema": map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}, "reason": map[string]any{"type": "string"}}, "required": []string{"ok"}, "additionalProperties": false}})}, Host: host}, Runtime: h.options.Runtime, ResolvedFastMode: &falseValue}
	var last *protocol.JSONObject
	err = h.consumeWebModel(ctx, scope, request, func(yield modelyield.Yield) error {
		if yield.Update != nil && yield.Update.Envelope != nil {
			data, err := yield.Update.Envelope.MessageJSON()
			if err != nil {
				return err
			}
			var message protocol.JSONObject
			if err = message.UnmarshalJSON(data); err != nil {
				return err
			}
			last = &message
		}
		return nil
	}, h.nativePromptHookModels)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if err = scope.AwaitFulfilled(); err != nil {
		return protocol.JSONObject{}, err
	}
	if ctx.Err() != nil {
		return protocol.JSONObject{}, ctx.Err()
	}
	if last == nil {
		return protocol.JSONObject{}, errors.New("No assistant message found")
	}
	return last.Clone(), nil
}
