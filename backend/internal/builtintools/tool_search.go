package builtintools

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolsearch"
)

type ToolSearchBindings struct {
	Service *toolsearch.Service
	Enabled func(*cooperative.Scope) (bool, error)
}

func (s *Service) HasToolSearchBindings() bool {
	return s != nil && s.options.ToolSearch != nil && s.options.ToolSearch.Service != nil && s.options.ToolSearch.Enabled != nil
}

func (s *Service) startToolSearch(ctx context.Context, in protocol.JSONObject, call queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
	scope := scopeFor(call.Opaque)
	if e := s.check(scope); e != nil {
		return nil, e
	}
	if e := context.Cause(ctx); e != nil {
		return nil, e
	}
	if !s.HasToolSearchBindings() {
		return nil, capability("actual shared tool search service")
	}
	actual, ok := call.Opaque.(*builtinwrite.CallContext)
	if !ok || actual.ToolSearchCatalogue == nil {
		return nil, capability("actual owning Host filtered tool catalogue")
	}
	data, e := s.options.ToolSearch.Service.Call(ctx, in, actual.ToolSearchCatalogue.Tools, actual.ToolSearchCatalogue.PendingServers)
	future, failure := ownerasync.NewOwnerFuture[queryloop.ToolCallResult](s.options.Owner)
	if failure != nil {
		return nil, failure
	}
	if e != nil {
		e = future.Reject(scope, e)
	} else {
		e = future.Resolve(scope, queryloop.ToolCallResult{Data: raw(data)})
	}
	return future, e
}
func mapToolSearch(data, id json.RawMessage) (protocol.JSONObject, error) {
	var content protocol.JSONObject
	if e := content.UnmarshalJSON(data); e != nil {
		return protocol.JSONObject{}, e
	}
	return toolsearch.MapResult(content, id)
}
