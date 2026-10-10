package sdkhost

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

const syntheticPrompt = "Use this tool to return your final response in the requested structured format. You MUST call this tool exactly once at the end of your response to provide the structured output."

// NativeSyntheticOutput is an actual transient worker Tool handler/definition,
// bound to that worker Owner and the real schema validator. It is not a static
// MCP peer or a replacement catalog/permission profile for the parent session.
type NativeSyntheticOutput struct {
	owner    *cooperative.Owner
	binding  *ownerasync.NativeBinding
	schema   json.RawMessage
	validate func(context.Context, protocol.JSONObject) error
}

func NewNativeSyntheticOutput(owner *cooperative.Owner, binding *ownerasync.NativeBinding, schema json.RawMessage, validate func(context.Context, protocol.JSONObject) error) (*NativeSyntheticOutput, error) {
	if owner == nil || binding == nil || !binding.Owns(owner) || validate == nil || !json.Valid(schema) {
		return nil, errors.New("StructuredOutput requires actual owner/schema/validator")
	}
	return &NativeSyntheticOutput{owner: owner, binding: binding, schema: append(json.RawMessage(nil), schema...), validate: validate}, nil
}
func (b *NativeSyntheticOutput) Owns(owner *cooperative.Owner) bool {
	return b != nil && b.owner == owner && b.binding != nil && b.binding.Owns(owner) && b.validate != nil
}
func (b *NativeSyntheticOutput) catalog(scope *cooperative.Scope) (toolschema.Definition, queryloop.RegisteredTool, error) {
	if !scope.BelongsTo(b.owner) {
		return toolschema.Definition{}, queryloop.RegisteredTool{}, cooperative.ErrRestricted
	}
	definition := toolschema.Definition{Kind: "registered", Name: "StructuredOutput", InputJSONSchema: append(json.RawMessage(nil), b.schema...), InputSchema: &toolschema.Schema{Kind: "object", UnknownKeys: "passthrough"}, Prompt: quote(syntheticPrompt), SearchHint: quote("return the final response as structured JSON")}
	tool := queryloop.RegisteredTool{Name: "StructuredOutput", PersistenceThreshold: 100000, SearchHint: definition.SearchHint, IsConcurrencySafe: func(protocol.JSONObject) bool { return true }, ParseInput: func(input protocol.JSONObject) (protocol.JSONObject, error) {
		if !input.IsObject() {
			data, _ := input.MarshalJSON()
			return protocol.JSONObject{}, toolschema.ObjectInputValidationError(data)
		}
		return input.Clone(), nil
	}, CallJSON: func(ctx context.Context, input protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
		scope := (*cooperative.Scope)(nil)
		if original, ok := call.Opaque.(*builtinwrite.CallContext); ok && original != nil {
			scope = original.Scope
		}
		if scope == nil || !scope.BelongsTo(b.owner) {
			return queryloop.ToolCallResult{}, cooperative.ErrRestricted
		}
		if e := b.validate(ctx, input); e != nil {
			return queryloop.ToolCallResult{}, e
		}
		return queryloop.ToolCallResult{Data: quote("Structured output provided successfully"), StructuredOutput: raw(input)}, nil
	}, MapResultJSON: func(data, id json.RawMessage) (protocol.JSONObject, error) {
		if _, e := protocol.DecodeJavaScriptJSONString(data); e != nil {
			return protocol.JSONObject{}, e
		}
		return protocol.NewJSONObject(protocol.JSONField{Name: "tool_use_id", Value: id}, protocol.JSONField{Name: "type", Value: quote("tool_result")}, protocol.JSONField{Name: "content", Value: data}), nil
	}}
	tool.StartCallJSON = b.start
	tool.CallJSON = func(ctx context.Context, input protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
		future, e := b.start(ctx, input, call)
		if e != nil {
			return queryloop.ToolCallResult{}, e
		}
		scope := (*cooperative.Scope)(nil)
		if original, ok := call.Opaque.(*builtinwrite.CallContext); ok && original != nil {
			scope = original.Scope
		}
		return future.Await(scope)
	}
	return definition, tool, nil
}
func (b *NativeSyntheticOutput) allows(owner *cooperative.Owner, tool queryloop.RegisteredTool) bool {
	return b.Owns(owner) && tool.Name == "StructuredOutput" && tool.CallJSON != nil && tool.MapResultJSON != nil
}

func (b *NativeSyntheticOutput) SyntheticToolNames(scope *cooperative.Scope) ([]string, error) {
	_, tool, e := b.catalog(scope)
	if e != nil {
		return nil, e
	}
	return []string{tool.Name}, nil
}
