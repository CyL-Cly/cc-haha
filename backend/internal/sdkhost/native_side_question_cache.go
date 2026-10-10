package sdkhost

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/sidequestion"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

// This is the original unprepared ASK context, not a rendered HTTP request.
// Saving it at Stop keeps the parent system/options cache prefix unchanged.
type nativeSideQuestionCache struct {
	request       requestretry.Input
	raw           sessioncontroller.RawContext
	systemContext protocol.JSONObject
	tracking      *queryloop.Tracking
}

func (h *Host) rememberSideQuestionCandidate(input requestretry.Input, raw sessioncontroller.RawContext, systemContext protocol.JSONObject, tracking queryloop.Tracking) {
	if h.options.Print != nil && h.options.Print.NativeProcess && (h.querySource() == "sdk" || h.querySource() == "repl_main_thread") {
		copy := nativeSideQuestionCache{input, raw, systemContext, &tracking}
		h.sideQuestionCandidate = &copy
	}
}
func (h *Host) saveSideQuestionCache() {
	if h.sideQuestionCandidate != nil {
		copy := *h.sideQuestionCandidate
		h.sideQuestionCache = &copy
	}
}
func (h *Host) sideQuestionSnapshot(scope *cooperative.Scope, in sidequestion.Input) (nativeSideQuestionCache, error) {
	if !scope.BelongsTo(h.options.Owner) || !h.nativeSideQuestionModels.Owns(h.options.Owner) {
		return nativeSideQuestionCache{}, cooperative.ErrRestricted
	}
	var captured nativeSideQuestionCache
	if h.sideQuestionCache != nil {
		captured = *h.sideQuestionCache
	} else {
		var e error
		captured, e = h.sideQuestionFallback(scope)
		if e != nil {
			return nativeSideQuestionCache{}, e
		}
	}
	history := []protocol.JSONObject{}
	for _, message := range h.serial.History() {
		data, e := message.MarshalJSON()
		if e != nil {
			return nativeSideQuestionCache{}, e
		}
		var value protocol.JSONObject
		if e = value.UnmarshalJSON(data); e != nil {
			return nativeSideQuestionCache{}, e
		}
		history = append(history, value)
	}
	prepared, e := sidequestion.Prepare(history, h.options.Factory)
	if e != nil {
		return nativeSideQuestionCache{}, e
	}
	suffix, e := sidequestion.PromptMessages(in, h.options.Factory)
	if e != nil {
		return nativeSideQuestionCache{}, e
	}
	captured.request.Context.History = append(prepared, suffix...)
	captured.request.Context.Options.QuerySource = "side_question"
	captured.request.Context.Options.SkipCacheWrite = true
	return captured, nil
}
func (h *Host) sideQuestionFallback(scope *cooperative.Scope) (nativeSideQuestionCache, error) {
	inference := h.captureInferenceSettings()
	deviceID, e := h.requestDeviceID(scope)
	if e != nil {
		return nativeSideQuestionCache{}, e
	}
	input := requestcontext.Context{Tools: h.originalTools, Thinking: h.options.Print.Thinking, Options: requestcontext.Options{Model: inference.model, QuerySource: "side_question", EffortValue: inference.effort, EffortValueOverridesEnv: inference.effortOverridesEnv, HasAppendSystemPrompt: h.options.Print.AppendSystemPrompt != ""}, Host: requestcontext.Host{Profile: requestcontext.NativeDirectRelayProfile, Env: h.requestEnvironment, DeviceID: deviceID, SessionID: h.options.Controller.SessionID(), SDKBetas: []string{}, TTLAllowlist: []string{}, ToolSchemas: h.toolSchemas}}
	if input.Thinking.Type == "" {
		input.Thinking.Type = "adaptive"
	}
	if h.options.Print.PlanProjection != nil {
		input.Host.PlanOptions, e = h.options.Print.PlanProjection(scope)
		if e != nil {
			return nativeSideQuestionCache{}, e
		}
	}
	definitions := append([]toolschema.Definition{}, h.definitions...)
	input.Host.NativeToolDefinitions = &definitions
	input.Host.NativeToolSearch = h.options.Print.ToolSearch
	input.Host.ToolSearchCatalogue = h.toolSearchCatalogue
	if h.options.NativeOpenAI != nil {
		input.Host.NativeCodexOAuth = h.options.NativeOpenAI(scope)
	}
	if h.options.NativeGrok != nil {
		input.Host.NativeGrokOAuth = h.options.NativeGrok(scope)
	}
	if h.options.Print.Tools != nil {
		input.Host.ToolSchemaHost, e = h.options.Print.Tools.SchemaHostForScope(scope, inference.model, h.options.OriginalEnv["ANTHROPIC_BASE_URL"])
		if e != nil {
			return nativeSideQuestionCache{}, e
		}
	}
	raw, e := h.serial.Raw(input)
	if e != nil {
		return nativeSideQuestionCache{}, e
	}
	if h.options.Print.AppendSystemPrompt != "" {
		raw.Context.SystemPrompt = append(append([]json.RawMessage{}, raw.Context.SystemPrompt...), quote(h.options.Print.AppendSystemPrompt))
	}
	resolved, e := h.serial.Resolve(context.Background(), raw, raw.Context.History)
	if e != nil {
		return nativeSideQuestionCache{}, e
	}
	systemContext := protocol.NewJSONObject()
	if h.options.Print.NativeSystemContext != nil {
		systemContext, e = h.options.Print.NativeSystemContext(scope)
		if e != nil {
			return nativeSideQuestionCache{}, e
		}
	}
	return nativeSideQuestionCache{requestretry.Input{Context: resolved, Runtime: h.options.Runtime}, raw, systemContext, nil}, nil
}
