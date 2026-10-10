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
	"github.com/NanmiCoder/cc-haha/backend/internal/websearch"
)

// NativeWebSearch uses the same queryModel producer and original inference
// settings. The external release feature's default selects the main model;
// unported live feature overrides remain guarded at host admission.
func (h *Host) NativeWebSearch(ctx context.Context, scope *cooperative.Scope, input protocol.JSONObject, model string, nonInteractive bool, progress websearch.Progress) ([]protocol.JSONObject, error) {
	if h == nil || !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess || h.options.Print.Tools == nil || !h.options.Print.Tools.HasWebSearchBindings() || model == "" {
		return nil, cap("actual native WebSearch model producer")
	}
	var result []protocol.JSONObject
	err := h.runNativeWebProducer(scope, ctx, "web_search_tool", func(scope *cooperative.Scope) error {
		var e error
		result, e = h.nativeWebSearchOnProducer(ctx, scope, input, model, nonInteractive, progress)
		return e
	})
	return result, err
}

func (h *Host) nativeWebSearchOnProducer(ctx context.Context, scope *cooperative.Scope, input protocol.JSONObject, model string, nonInteractive bool, progress websearch.Progress) ([]protocol.JSONObject, error) {
	prompt, e := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quote("Perform a web search for the query: "), input.Get("query")}, "")
	if e != nil {
		return nil, e
	}
	user := messages.OrderedUser(h.options.Factory.CreateUser(storage.Object{"content": prompt}))
	defs := []toolschema.Definition{}
	deviceID, e := h.requestDeviceID(scope)
	if e != nil {
		return nil, e
	}
	effort, _, e := h.InferenceEffort(scope)
	if e != nil {
		return nil, e
	}
	thinking := h.options.Print.Thinking
	if thinking.Type == "" {
		thinking.Type = "adaptive"
	}
	falseValue := false
	original := requestretry.Input{Context: requestcontext.Context{History: []protocol.JSONObject{user}, SystemPrompt: []json.RawMessage{quote("You are an assistant for performing a web search tool use")}, Tools: []protocol.JSONObject{}, Thinking: thinking, Options: requestcontext.Options{Model: model, QuerySource: "web_search_tool", IsNonInteractiveSession: nonInteractive, ExtraToolSchemas: []json.RawMessage{websearch.NativeToolSchema(input)}, EffortValue: effort, HasAppendSystemPrompt: false}, Host: requestcontext.Host{Profile: requestcontext.NativeDirectRelayProfile, Env: h.requestEnvironment, DeviceID: deviceID, SessionID: h.webModelSessionID(), NativeToolDefinitions: &defs, SDKBetas: []string{}, TTLAllowlist: []string{}}}, Runtime: h.options.Runtime, ResolvedFastMode: &falseValue}
	blocks := []protocol.JSONObject{}
	reducer := websearch.NewProgressReducer(input.Get("query"))
	e = h.consumeWebModel(ctx, scope, original, func(yield modelyield.Yield) error {
		if yield.Update != nil {
			if yield.Update.Envelope != nil {
				for _, block := range yield.Update.Envelope.Content {
					blocks = append(blocks, block.Object.Clone())
				}
			} else if yield.Update.Event != nil {
				var event protocol.JSONObject
				if e := json.Unmarshal(yield.Update.Event, &event); e != nil {
					return e
				}
				if e := reducer.Observe(event, progress); e != nil {
					return e
				}
			}
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return blocks, scope.AwaitFulfilled()
}
