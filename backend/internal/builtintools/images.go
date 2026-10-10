package builtintools

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/imagegeneration"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

type ImageBindings struct {
	Read              func(context.Context, *cooperative.Scope, string, float64) (*ownerasync.OwnerFuture[json.RawMessage], error)
	MetadataMessage   func(*cooperative.Scope, json.RawMessage) (*queryloop.Message, error)
	Config            func(*cooperative.Scope) (*imagegeneration.Config, error)
	Generate          func(context.Context, *cooperative.Scope, protocol.JSONObject, imagegeneration.Config) (imagegeneration.Output, error)
	RegisterUserImage func(context.Context, *cooperative.Scope, string) error
}

func imageTool(name string) bool { return name == "ImageGen" || name == "ImageEdit" }
func (s *Service) HasImageBindings() bool {
	return s != nil && s.options.Images != nil && s.options.Images.Config != nil && s.options.Images.Generate != nil && s.options.Images.RegisterUserImage != nil
}
func (s *Service) ImageToolsEnabled(scope *cooperative.Scope) (bool, error) {
	if err := s.check(scope); err != nil {
		return false, err
	}
	if !s.HasImageBindings() {
		return false, nil
	}
	config, err := s.options.Images.Config(scope)
	return config != nil, err
}
func (s *Service) callImage(ctx context.Context, scope *cooperative.Scope, input protocol.JSONObject) (json.RawMessage, error) {
	if err := s.check(scope); err != nil {
		return nil, err
	}
	if !s.HasImageBindings() {
		return nil, capability("actual image file/auth/fetch producer")
	}
	config, err := s.options.Images.Config(scope)
	if err != nil {
		return nil, err
	}
	if config == nil {
		return nil, capability("enabled session image provider")
	}
	output, err := s.options.Images.Generate(ctx, scope, input, *config)
	if err != nil {
		return nil, err
	}
	return json.Marshal(output)
}
func (s *Service) startImage(ctx context.Context, input protocol.JSONObject, call queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
	scope := scopeFor(call.Opaque)
	if err := s.check(scope); err != nil {
		return nil, err
	}
	future, err := ownerasync.NewOwnerFuture[queryloop.ToolCallResult](s.options.Owner)
	if err != nil {
		return nil, err
	}
	_, err = s.options.Owner.SubmitIndependent(ctx, cooperative.Read, func(next *cooperative.Scope) error {
		data, err := s.callImage(ctx, next, input)
		if err != nil {
			return future.Reject(next, err)
		}
		return future.Resolve(next, queryloop.ToolCallResult{Data: data})
	})
	if err != nil {
		_ = future.RejectAdministrative(err)
		return nil, err
	}
	return future, nil
}
func imagePermission(input *protocol.JSONObject) queryloop.ToolPermission {
	return queryloop.ToolPermission{Behavior: "allow", Input: input, UpdatedInput: input}
}
func (s *Service) readResult(scope *cooperative.Scope, data json.RawMessage) (queryloop.ToolCallResult, error) {
	result := queryloop.ToolCallResult{Data: data}
	if s.options.Images == nil || s.options.Images.MetadataMessage == nil {
		return result, nil
	}
	message, err := s.options.Images.MetadataMessage(scope, data)
	if err != nil {
		return result, err
	}
	if message != nil {
		result.NewMessages = []queryloop.Message{*message}
	}
	return result, nil
}
func mapImageResult(data, id json.RawMessage) (protocol.JSONObject, error) {
	serialized, err := protocol.ParseAndStringifyJavaScriptJSON(data)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "tool_use_id", Value: id}, field("type", "tool_result"), field("content", string(serialized))), nil
}
