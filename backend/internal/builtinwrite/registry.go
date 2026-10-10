package builtinwrite

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

// Catalog is only the migrated Write contribution to the caller's registry.
// It is not the full Source catalog. Bare has no Write, and whole-tool denies
// filter registration before rendering. Root still migrates the other tools.
func (s *Service) Catalog(scope *cooperative.Scope) ([]toolschema.Definition, []queryloop.RegisteredTool, error) {
	if e := s.check(scope); e != nil {
		return nil, nil, e
	}
	if truth(s.options.Environment("CLAUDE_CODE_SIMPLE")) {
		return []toolschema.Definition{}, []queryloop.RegisteredTool{}, nil
	}
	permissionContext, e := s.options.PermissionContext(scope)
	if e != nil {
		return nil, nil, e
	}
	deny, _, e := s.rules(scope, permissionContext, "", true)
	if e != nil {
		return nil, nil, e
	}
	if deny {
		return []toolschema.Definition{}, []queryloop.RegisteredTool{}, nil
	}
	definition, e := toolschema.NativeBuiltinDefinition("Write")
	if e != nil {
		return nil, nil, e
	}
	tool := queryloop.RegisteredTool{Name: "Write", ParseInput: func(input protocol.JSONObject) (protocol.JSONObject, error) {
		return toolschema.ParseInput(definition, input)
	}, IsConcurrencySafe: func(protocol.JSONObject) bool { return false }, Backfill: s.Backfill, PersistenceThreshold: 100000,
		ValidateInput: func(ctx context.Context, input protocol.JSONObject, opaque any) error {
			return s.Validate(ctx, input, scopeFor(opaque))
		},
		CallJSON: func(ctx context.Context, input protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
			var parent json.RawMessage
			if call.Invocation.Assistant != nil {
				parent = call.Invocation.Assistant.Record.Get("uuid")
			}
			data, e := s.callWithParent(ctx, input, scopeFor(call.Opaque), parent)
			return queryloop.ToolCallResult{Data: data}, e
		}, MapResultJSON: MapResultJSON}
	return []toolschema.Definition{definition}, []queryloop.RegisteredTool{tool}, nil
}
func MapResultJSON(data json.RawMessage, toolUseID json.RawMessage) (protocol.JSONObject, error) {
	if _, e := protocol.DecodeJavaScriptJSONString(toolUseID); e != nil {
		return protocol.JSONObject{}, e
	}
	var result protocol.JSONObject
	e := json.Unmarshal(data, &result)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	kind, e := scalar(result.Get("type"))
	if e != nil {
		return protocol.JSONObject{}, e
	}
	path, e := protocol.DecodeJavaScriptJSONString(result.Get("filePath"))
	if e != nil {
		return protocol.JSONObject{}, e
	}
	prefix, suffix := "", ""
	switch kind {
	case "create":
		prefix = "File created successfully at: "
	case "update":
		prefix = "The file "
		suffix = " has been updated successfully."
	default:
		return protocol.JSONObject{}, capability("unknown native Write result type")
	}
	p, _ := protocol.DecodeJavaScriptJSONString(quoted(prefix))
	q, _ := protocol.DecodeJavaScriptJSONString(quoted(suffix))
	units := append(p, path...)
	units = append(units, q...)
	return protocol.NewJSONObject(protocol.JSONField{Name: "tool_use_id", Value: toolUseID}, protocol.JSONField{Name: "type", Value: quoted("tool_result")}, protocol.JSONField{Name: "content", Value: contentRaw(units)}), nil
}
