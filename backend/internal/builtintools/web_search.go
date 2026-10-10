package builtintools

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/websearch"
)

type WebSearchBindings struct {
	Search    *websearch.Service
	Model     func(*cooperative.Scope) (string, error)
	Native    func(context.Context, *cooperative.Scope, protocol.JSONObject, string, bool, websearch.Progress) ([]protocol.JSONObject, error)
	MonthYear func() string
}

func (s *Service) HasWebSearchBindings() bool {
	return s != nil && s.options.WebSearch != nil && s.options.WebSearch.Search != nil && s.options.WebSearch.Model != nil && s.options.WebSearch.Native != nil && s.options.WebSearch.MonthYear != nil
}
func (s *Service) webSearchEnabled(scope *cooperative.Scope) (bool, error) {
	if !s.HasWebSearchBindings() {
		return false, nil
	}
	model, e := s.options.WebSearch.Model(scope)
	if e != nil {
		return false, e
	}
	return s.options.WebSearch.Search.Enabled(model)
}
func (s *Service) startWebSearch(ctx context.Context, in protocol.JSONObject, call queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
	scope := scopeFor(call.Opaque)
	if e := s.check(scope); e != nil {
		return nil, e
	}
	if !s.HasWebSearchBindings() {
		return nil, capability("actual WebSearch producers")
	}
	model, e := s.options.WebSearch.Model(scope)
	if c, ok := call.Opaque.(*builtinwrite.CallContext); ok && c.MainLoopModel != "" {
		model = c.MainLoopModel
		e = nil
	}
	if e != nil {
		return nil, e
	}
	output, e := ownerasync.NewOwnerFuture[queryloop.ToolCallResult](s.options.Owner)
	if e != nil {
		return nil, e
	}
	_, e = s.options.Owner.SubmitIndependent(ctx, cooperative.Read, func(next *cooperative.Scope) error {
		out, e := s.options.WebSearch.Search.Call(ctx, in, model, func(ctx context.Context, input protocol.JSONObject, progress websearch.Progress) ([]protocol.JSONObject, error) {
			return s.options.WebSearch.Native(ctx, next, input, model, s.options.NonInteractive, progress)
		}, func(ctx context.Context, provider string, input protocol.JSONObject, key string) (websearch.Output, error) {
			future, e := native(s, next, func() (websearch.Output, error) {
				return s.options.WebSearch.Search.SearchExternal(ctx, provider, input, key)
			})
			if e != nil {
				return websearch.Output{}, e
			}
			return future.Await(next)
		}, call.OnProgress)
		if e != nil {
			return output.Reject(next, e)
		}
		return output.Resolve(next, queryloop.ToolCallResult{Data: raw(out)})
	})
	if e != nil {
		_ = output.RejectAdministrative(e)
		return nil, e
	}
	return output, nil
}
func validateWebSearch(in protocol.JSONObject) error {
	units, e := protocol.DecodeJavaScriptJSONString(in.Get("query"))
	if e != nil {
		return e
	}
	if len(units) == 0 {
		return errors.New("Error: Missing query")
	}
	var allowed, blocked []json.RawMessage
	_ = json.Unmarshal(in.Get("allowed_domains"), &allowed)
	_ = json.Unmarshal(in.Get("blocked_domains"), &blocked)
	if len(allowed) > 0 && len(blocked) > 0 {
		return errors.New("Error: Cannot specify both allowed_domains and blocked_domains in the same request")
	}
	return nil
}
func webSearchDecision(in *protocol.JSONObject) queryloop.ToolPermission {
	return queryloop.ToolPermission{Behavior: "ask", Input: in, Message: quote("WebSearchTool requires permission."), Description: raw("Claude wants to search the web for: " + in.String("query")), Suggestions: raw([]protocol.JSONObject{protocol.NewJSONObject(field("type", "addRules"), field("rules", []protocol.JSONObject{protocol.NewJSONObject(field("toolName", "WebSearch"))}), field("behavior", "allow"), field("destination", "localSettings"))})}
}
