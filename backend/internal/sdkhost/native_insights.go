package sdkhost

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelyield"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

// QueryInsights retains queryWithModel's single-input no-tools request and
// complete native iterator settlement. The authority is issued by the actual
// canonical Host; a caller cannot use a querySource string to unlock transport.
func (h *Host) QueryInsights(ctx context.Context, scope *cooperative.Scope, prompt json.RawMessage, model string, maxTokens float64) (json.RawMessage, error) {
	if h == nil || !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess || !h.nativeInsightsModels.Owns(h.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	var output json.RawMessage
	err := h.nativeInsightsModels.Run(scope, ctx, func(producer *cooperative.Scope) error {
		deviceID, err := h.requestDeviceID(producer)
		if err != nil {
			return err
		}
		// withVCR allocates these two recording keys before the actual query body.
		_ = h.options.Factory.CreateUser(storage.Object{"content": raw([]protocol.JSONObject{})})
		_ = h.options.Factory.CreateUser(storage.Object{"content": prompt})
		user := messages.OrderedUser(h.options.Factory.CreateUser(storage.Object{"content": prompt}))
		disabled := false
		definitions := []toolschema.Definition{}
		input := requestretry.Input{Context: requestcontext.Context{History: []protocol.JSONObject{user}, SystemPrompt: []json.RawMessage{}, Tools: []protocol.JSONObject{}, Thinking: requestcontext.Thinking{Type: "disabled"}, Options: requestcontext.Options{Model: model, QuerySource: "insights", IsNonInteractiveSession: true, HasAppendSystemPrompt: false, EnablePromptCaching: &disabled, MaxOutputTokensOverride: &maxTokens}, Host: requestcontext.Host{Profile: requestcontext.NativeDirectRelayProfile, Env: h.requestEnvironment, DeviceID: deviceID, SessionID: h.options.Controller.SessionID(), NativeToolDefinitions: &definitions, SDKBetas: []string{}, TTLAllowlist: []string{}}}, Runtime: h.options.Runtime, ResolvedFastMode: &disabled}
		var last json.RawMessage
		err = h.consumeInsightsModel(ctx, producer, input, func(value modelyield.Yield) error {
			if value.Update != nil && value.Update.Envelope != nil {
				var err error
				last, err = value.Update.Envelope.MessageJSON()
				return err
			}
			return nil
		})
		if err != nil {
			return err
		}
		if err = producer.AwaitFulfilled(); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		if len(last) == 0 {
			return errors.New("No assistant message found")
		}
		var message protocol.JSONObject
		_ = message.UnmarshalJSON(last)
		var content []protocol.JSONObject
		if err = json.Unmarshal(message.Get("content"), &content); err != nil {
			return err
		}
		texts := []json.RawMessage{}
		for _, block := range content {
			if block.String("type") == "text" {
				texts = append(texts, block.Get("text"))
			}
		}
		output, err = protocol.JoinJavaScriptValuesJSON(texts, "")
		return err
	})
	return output, err
}
