package requestretry

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func RunNativeAutoCritique(ctx context.Context, input Input, settings protocol.JSONObject, opts requestcontext.SideQueryInput, deps Dependencies, authority *cooperative.NativeAutoCritiqueModels) (provider.MessageResponse, error) {
	if authority == nil || !authority.Owns(deps.StreamOwner) || deps.StreamScope == nil || !deps.StreamScope.BelongsTo(deps.StreamOwner) || input.Context.Host.Profile != requestcontext.NativeDirectRelayProfile || input.Context.Options.QuerySource != "auto_mode_critique" || input.Context.Options.Model == "" {
		return provider.MessageResponse{}, cooperative.ErrRestricted
	}
	if deps.NativeOpenIO == nil || deps.BodyIOAwait == nil || deps.OpenIOAwait != nil {
		return provider.MessageResponse{}, unsupported("auto-mode critique current native owner headers/body")
	}
	if err := deps.NativeOpenIO.CheckOwner(); err != nil {
		return provider.MessageResponse{}, err
	}
	if err := BindNativeClaudeHost(ctx, &input, deps); err != nil {
		return provider.MessageResponse{}, err
	}
	if err := BindNativeVertexHost(&input, deps); err != nil {
		return provider.MessageResponse{}, err
	}
	if err := BindNativeBedrockHost(&input, deps); err != nil {
		return provider.MessageResponse{}, err
	}
	input.Context.Host.NativeCodexOAuth = deps.NativeCodexOAuth
	input.Context.Host.NativeGrokOAuth = deps.NativeGrokOAuth
	params, err := requestcontext.NativeSideQueryRequest(input.Context, settings, opts)
	if err != nil {
		return provider.MessageResponse{}, err
	}
	env := map[string]string{}
	for key, value := range input.Context.Host.Env {
		if key != "CLAUDE_CODE_SIMPLE" && key != "CLAUDE_CODE_DISABLE_FAST_MODE" {
			env[key] = value
		}
	}
	var prepared *provider.MessagesRequest
	client, err := originalClient(ctx, input, deps, env, input.Context, func() *provider.MessagesRequest { return prepared })
	if err != nil {
		return provider.MessageResponse{}, err
	}
	return client.WithNativeSDKRetries(ctx, 2, provider.AuxiliaryRetry{Now: deps.Now, Random: deps.Random, Sleep: deps.Sleep}, func(c *provider.Anthropic) (provider.MessageResponse, error) {
		return c.CreateNativeSideQuery(ctx, params, func(actual *provider.MessagesRequest) { prepared = actual })
	})
}
