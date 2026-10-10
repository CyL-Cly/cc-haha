package sdkhost

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelyield"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

// ApplyWebFetchPrompt mirrors queryHaiku/queryModelWithoutStreaming. The
// physical request remains streamed; the caller consumes it fully and returns
// only the last assistant's first text block. It shares process accounting and
// credentials but does not append an auxiliary prompt to the main transcript.
func (h *Host) ApplyWebFetchPrompt(ctx context.Context, scope *cooperative.Scope, prompt json.RawMessage, model string, nonInteractive bool) (json.RawMessage, error) {
	if h == nil || !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess || h.options.Print.Tools == nil || !h.options.Print.Tools.HasWebFetchBindings() || model == "" {
		return nil, cap("actual WebFetch secondary-model bindings")
	}
	var result json.RawMessage
	err := h.runNativeWebProducer(scope, ctx, "web_fetch_apply", func(scope *cooperative.Scope) error {
		var e error
		result, e = h.applyWebFetchPromptOnProducer(ctx, scope, prompt, model, nonInteractive)
		return e
	})
	return result, err
}

func (h *Host) applyWebFetchPromptOnProducer(ctx context.Context, scope *cooperative.Scope, prompt json.RawMessage, model string, nonInteractive bool) (json.RawMessage, error) {
	user := messages.OrderedUser(h.options.Factory.CreateUser(storage.Object{"content": prompt}))
	disabled := false
	defs := []toolschema.Definition{}
	deviceID, err := h.requestDeviceID(scope)
	if err != nil {
		return nil, err
	}
	input := requestretry.Input{Context: requestcontext.Context{History: []protocol.JSONObject{user}, SystemPrompt: []json.RawMessage{}, Tools: []protocol.JSONObject{}, Thinking: requestcontext.Thinking{Type: "disabled"}, Options: requestcontext.Options{Model: model, QuerySource: "web_fetch_apply", IsNonInteractiveSession: nonInteractive, EnablePromptCaching: &disabled}, Host: requestcontext.Host{Profile: requestcontext.NativeDirectRelayProfile, Env: h.requestEnvironment, DeviceID: deviceID, SessionID: h.webModelSessionID(), NativeToolDefinitions: &defs, SDKBetas: []string{}, TTLAllowlist: []string{}}}, Runtime: h.options.Runtime, ResolvedFastMode: &disabled}
	var last json.RawMessage
	err = h.consumeWebModel(ctx, scope, input, func(yield modelyield.Yield) error {
		if yield.Update != nil && yield.Update.Envelope != nil {
			var e error
			last, e = yield.Update.Envelope.MessageJSON()
			if e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err = scope.AwaitFulfilled(); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var message protocol.JSONObject
	_ = json.Unmarshal(last, &message)
	var content []protocol.JSONObject
	_ = json.Unmarshal(message.Get("content"), &content)
	if len(content) > 0 && content[0].Get("text") != nil {
		return content[0].Get("text"), nil
	}
	return quote("No response from model"), nil
}
