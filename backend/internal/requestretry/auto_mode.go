package requestretry

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

// RunNativeAutoModeQuery opens exactly one original side-query JSON call. A
// querySource string cannot admit it, and the query retry loop is not used.
func RunNativeAutoModeQuery(ctx context.Context, input Input, settings protocol.JSONObject, opts requestcontext.SideQueryInput, deps Dependencies, authority *cooperative.NativeAutoModeModels) (provider.MessageResponse, error) {
	if authority == nil || !authority.Owns(deps.StreamOwner) || deps.StreamScope == nil || !deps.StreamScope.BelongsTo(deps.StreamOwner) || input.Context.Host.Profile != requestcontext.NativeDirectRelayProfile || input.Context.Options.QuerySource != "auto_mode" || input.Context.Options.Model == "" {
		return provider.MessageResponse{}, cooperative.ErrRestricted
	}
	if deps.NativeOpenIO == nil || deps.BodyIOAwait == nil || deps.OpenIOAwait != nil {
		return provider.MessageResponse{}, unsupported("auto-mode current native owner headers/body")
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
	for k, v := range input.Context.Host.Env {
		if k != "CLAUDE_CODE_SIMPLE" && k != "CLAUDE_CODE_DISABLE_FAST_MODE" {
			env[k] = v
		}
	}
	var prepared *provider.MessagesRequest
	client, err := originalClient(ctx, input, deps, env, input.Context, func() *provider.MessagesRequest { return prepared })
	if err != nil {
		return provider.MessageResponse{}, err
	}
	max := float64(10)
	if value := env["CLAUDE_CODE_MAX_RETRIES"]; value != "" {
		max = provider.ParseSDKRetryCount(value)
	}
	return client.WithNativeSDKRetries(ctx, max, provider.AuxiliaryRetry{Now: deps.Now, Random: deps.Random, Sleep: deps.Sleep}, func(c *provider.Anthropic) (provider.MessageResponse, error) {
		return c.CreateNativeSideQuery(ctx, params, func(actual *provider.MessagesRequest) { prepared = actual })
	})
}
