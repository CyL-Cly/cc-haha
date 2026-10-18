package sdkhost

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

// fetchSystemPromptParts awaits Promise.all of the actual default prompt,
// memoized user context and system context before QueryEngine processes input.
// Even a custom prompt has three fulfilled member reactions: [] / user / {}.
func (h *Host) nativeFetchSystemPromptParts(scope *cooperative.Scope, model string) (sessioncontroller.RawContext, protocol.JSONObject, error) {
	if h.options.Controller.HasNativePendingContext() && h.options.Print.BeginNativeSystemContext != nil {
		return h.nativeFetchPendingSystemPromptParts(scope, model)
	}
	prefix, err := h.options.Controller.ResolveRawContextOnOwner(scope, requestcontext.Context{Tools: h.originalTools, Options: requestcontext.Options{Model: model}})
	if err != nil {
		return sessioncontroller.RawContext{}, protocol.JSONObject{}, err
	}
	system := protocol.NewJSONObject()
	if h.options.Print.NativeSystemContext != nil {
		system, err = h.options.Print.NativeSystemContext(scope)
		if err != nil {
			return sessioncontroller.RawContext{}, protocol.JSONObject{}, err
		}
	}
	type parts struct {
		prefix sessioncontroller.RawContext
		system protocol.JSONObject
		prompt []json.RawMessage
	}
	all, err := NewOwnerFuture[parts](h.options.Owner)
	if err != nil {
		return sessioncontroller.RawContext{}, protocol.JSONObject{}, err
	}
	defaultPrompt, err := NewOwnerFuture[[]json.RawMessage](h.options.Owner)
	if err != nil {
		return sessioncontroller.RawContext{}, protocol.JSONObject{}, err
	}
	userContext, err := NewOwnerFuture[sessioncontroller.RawContext](h.options.Owner)
	if err != nil {
		return sessioncontroller.RawContext{}, protocol.JSONObject{}, err
	}
	systemContext, err := NewOwnerFuture[protocol.JSONObject](h.options.Owner)
	if err != nil {
		return sessioncontroller.RawContext{}, protocol.JSONObject{}, err
	}
	if err = defaultPrompt.Resolve(scope, prefix.Context.SystemPrompt); err != nil {
		return sessioncontroller.RawContext{}, protocol.JSONObject{}, err
	}
	if err = userContext.Resolve(scope, prefix); err != nil {
		return sessioncontroller.RawContext{}, protocol.JSONObject{}, err
	}
	if err = systemContext.Resolve(scope, system); err != nil {
		return sessioncontroller.RawContext{}, protocol.JSONObject{}, err
	}
	combined, remaining := parts{}, 3
	settleMember := func(next *cooperative.Scope, failure error) error {
		if failure != nil {
			return all.Reject(next, failure)
		}
		remaining--
		if remaining == 0 {
			return all.Resolve(next, combined)
		}
		return nil
	}
	// Promise.all observes the three actual producer values independently;
	// the aggregate returns those same values, with no scheduling-only member.
	if _, err = defaultPrompt.Then(scope, func(next *cooperative.Scope, value []json.RawMessage, failure error) error {
		combined.prompt = value
		return settleMember(next, failure)
	}); err != nil {
		return sessioncontroller.RawContext{}, protocol.JSONObject{}, err
	}
	if _, err = userContext.Then(scope, func(next *cooperative.Scope, value sessioncontroller.RawContext, failure error) error {
		combined.prefix = value
		return settleMember(next, failure)
	}); err != nil {
		return sessioncontroller.RawContext{}, protocol.JSONObject{}, err
	}
	if _, err = systemContext.Then(scope, func(next *cooperative.Scope, value protocol.JSONObject, failure error) error {
		combined.system = value
		return settleMember(next, failure)
	}); err != nil {
		return sessioncontroller.RawContext{}, protocol.JSONObject{}, err
	}
	result, err := all.Await(scope)
	if err != nil {
		return sessioncontroller.RawContext{}, protocol.JSONObject{}, err
	}
	prefix, system = result.prefix, result.system
	prefix.Context.SystemPrompt = result.prompt
	// The caller awaits fetchSystemPromptParts's async return, independently
	// of the aggregate's own await, before entering processUserInput.
	if err = scope.AwaitFulfilled(); err != nil {
		return sessioncontroller.RawContext{}, protocol.JSONObject{}, err
	}
	return prefix, system, nil
}

func (h *Host) nativeFetchPendingSystemPromptParts(scope *cooperative.Scope, model string) (sessioncontroller.RawContext, protocol.JSONObject, error) {
	pending, err := h.options.Controller.BeginRawContextOnOwner(scope, requestcontext.Context{Tools: h.originalTools, Options: requestcontext.Options{Model: model}})
	if err != nil {
		return sessioncontroller.RawContext{}, protocol.JSONObject{}, err
	}
	// The third array member starts before we observe any of the three results.
	system, err := h.options.Print.BeginNativeSystemContext(scope)
	if err != nil {
		return sessioncontroller.RawContext{}, protocol.JSONObject{}, err
	}
	all, err := NewOwnerFuture[struct{}](h.options.Owner)
	if err != nil {
		return sessioncontroller.RawContext{}, protocol.JSONObject{}, err
	}
	remaining := 3
	value := protocol.NewJSONObject()
	settle := func(next *cooperative.Scope, failure error) error {
		if failure != nil {
			return all.Reject(next, failure)
		}
		remaining--
		if remaining == 0 {
			return all.Resolve(next, struct{}{})
		}
		return nil
	}
	if _, err = pending.DefaultPrompt().Then(scope, func(next *cooperative.Scope, _ []json.RawMessage, failure error) error { return settle(next, failure) }); err != nil {
		return sessioncontroller.RawContext{}, protocol.JSONObject{}, err
	}
	if _, err = pending.UserContext().Then(scope, func(next *cooperative.Scope, _ *protocol.JSONObject, failure error) error {
		return settle(next, failure)
	}); err != nil {
		return sessioncontroller.RawContext{}, protocol.JSONObject{}, err
	}
	if _, err = system.Then(scope, func(next *cooperative.Scope, result protocol.JSONObject, failure error) error {
		value = result
		return settle(next, failure)
	}); err != nil {
		return sessioncontroller.RawContext{}, protocol.JSONObject{}, err
	}
	if _, err = all.Await(scope); err != nil {
		return sessioncontroller.RawContext{}, protocol.JSONObject{}, err
	}
	prefix, err := pending.CompleteOnOwner(scope)
	if err != nil {
		return sessioncontroller.RawContext{}, protocol.JSONObject{}, err
	}
	if err = scope.AwaitFulfilled(); err != nil {
		return sessioncontroller.RawContext{}, protocol.JSONObject{}, err
	}
	return prefix, value, nil
}
