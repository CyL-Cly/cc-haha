package requestretry

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

// ValidateNativeModel opens exactly one original side-query JSON call. A
// querySource string cannot admit it, and the query retry loop is not used.
func ValidateNativeModel(ctx context.Context, input Input, settings protocol.JSONObject, deps Dependencies, authority *cooperative.NativeModelValidationModels) error {
	if authority == nil || !authority.Owns(deps.StreamOwner) || deps.StreamScope == nil || !deps.StreamScope.BelongsTo(deps.StreamOwner) || input.Context.Host.Profile != requestcontext.NativeDirectRelayProfile || input.Context.Options.QuerySource != "model_validation" || input.Context.Options.Model == "" {
		return cooperative.ErrRestricted
	}
	if deps.NativeOpenIO == nil || deps.BodyIOAwait == nil || deps.OpenIOAwait != nil {
		return unsupported("model validation current native owner headers/body")
	}
	if err := deps.NativeOpenIO.CheckOwner(); err != nil {
		return err
	}
	if err := BindNativeClaudeHost(ctx, &input, deps); err != nil {
		return err
	}
	if err := BindNativeVertexHost(&input, deps); err != nil {
		return err
	}
	if err := BindNativeBedrockHost(&input, deps); err != nil {
		return err
	}
	input.Context.Host.NativeCodexOAuth = deps.NativeCodexOAuth
	input.Context.Host.NativeGrokOAuth = deps.NativeGrokOAuth
	params, err := requestcontext.NativeModelValidationRequest(input.Context, settings)
	if err != nil {
		return err
	}
	env := map[string]string{}
	for k, v := range input.Context.Host.Env {
		if k != "CLAUDE_CODE_SIMPLE" && k != "CLAUDE_CODE_MAX_RETRIES" && k != "CLAUDE_CODE_DISABLE_FAST_MODE" {
			env[k] = v
		}
	}
	var prepared *provider.MessagesRequest
	client, err := originalClient(ctx, input, deps, env, input.Context, func() *provider.MessagesRequest { return prepared })
	if err != nil {
		return err
	}
	_, err = client.CreateModelValidationMessageObserved(ctx, params, func(actual *provider.MessagesRequest) { prepared = actual })
	return err
}
