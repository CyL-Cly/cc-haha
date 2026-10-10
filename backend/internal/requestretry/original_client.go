package requestretry

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/tracecapture"
	"net/http"
)

// originalClient is the unchanged original GetClient constructor. Both the
// query stream and the separately sealed model-validation JSON consumer bind
// their current producer scope and original auth inputs here.
func originalClient(ctx context.Context, input Input, deps Dependencies, clientEnv map[string]string, request requestcontext.Context, getPrepared func() *provider.MessagesRequest) (*provider.Anthropic, error) {
	var nativeOwner *provider.OwnerNativeIO
	if deps.NativeOpenIO != nil {
		var failure error
		nativeOwner, failure = deps.NativeOpenIO.NewNativeOpenIO()
		if failure != nil {
			return nil, failure
		}
		if nativeOwner == nil {
			return nil, unsupported("missing original native owner bridge")
		}
		if failure = nativeOwner.CheckOwner(); failure != nil {
			return nil, failure
		}
	}
	if deps.OnKeyLookup != nil {
		deps.OnKeyLookup()
	}
	grok, err := selectGrok(ctx, input)
	if err != nil {
		return nil, err
	}
	codex, err := selectOpenAICodex(ctx, input)
	if err != nil {
		return nil, err
	}
	var key *string
	var helperToken *string
	var claudeToken *string
	if grok != nil {
		dummy := provider.GrokOAuthDummyKey
		key = &dummy
	} else if codex != nil {
		dummy := providerproxy.OpenAIOAuthDummyKey
		key = &dummy
	} else if input.Context.Host.NativeClaudeSubscriber && deps.NativeClaudeAuth.Owns(deps.StreamOwner, deps.StreamScope) {
		claudeToken, err = deps.NativeClaudeAuth.Token(ctx)
	} else if deps.NativeAuthHelper != nil {
		resolved, failure := deps.NativeAuthHelper.resolve(ctx, input.Context.Options.IsNonInteractiveSession)
		key, helperToken, err = resolved.Key, resolved.Token, failure
		if err == nil && !resolved.Configured && key == nil && helperToken == nil {
			key, err = resolveOriginalEnvKey(input)
		}
	} else {
		key, err = resolveOriginalEnvKey(input)
	}
	if err != nil {
		return nil, err
	}
	if deps.OnClient != nil {
		deps.OnClient()
	}
	config := provider.ResolveDirectClientOptions(provider.DirectClientInput{Env: clientEnv, FallbackAPIKey: key, SessionID: request.Host.SessionID, Workload: request.Host.Workload, Runtime: input.Runtime, RequestID: deps.UUID}).StreamConfig()
	if helperToken != nil && *helperToken != "" {
		config.DefaultHeaders.Set("Authorization", "Bearer "+*helperToken)
	}
	if claudeToken != nil {
		config.APIKey, config.APIKeySet = "", true
		if request.Options.QuerySource == "model_validation" {
			config.APIKeySet = false
		}
		config.AuthToken, config.AuthTokenSet = *claudeToken, true
	}
	config.OpenAICodex = codex
	config.GrokOAuth = grok
	if input.Context.Host.NativeVertexBound {
		if grok != nil || codex != nil {
			return nil, unsupported("Vertex combined with active OAuth fetch wrapper")
		}
		cloud, failure := deps.NativeVertex.Client(ctx, request.Options.Model)
		if failure != nil {
			return nil, failure
		}
		if cloud.Config == nil {
			return nil, unsupported("actual Vertex client producer")
		}
		config.Vertex = cloud.Config
		config.BaseURL = cloud.BaseURL
	}
	if input.Context.Host.NativeBedrockBound {
		if grok != nil || codex != nil {
			return nil, unsupported("Bedrock combined with active OAuth fetch wrapper")
		}
		cloud, failure := deps.NativeBedrock.Client(ctx, request.Options.Model)
		if failure != nil {
			return nil, failure
		}
		if cloud.Config == nil {
			return nil, unsupported("actual Bedrock client producer")
		}
		config.Bedrock = cloud.Config
		config.BaseURL = cloud.BaseURL
		if cloud.BearerToken != "" {
			config.DefaultHeaders.Set("Authorization", "Bearer "+cloud.BearerToken)
		}
	}
	if grok != nil {
		config.OpenAICodex = nil
		config.APIKey, config.APIKeySet = provider.GrokOAuthDummyKey, true
		config.FinalizeHeaders = nil
	}
	if codex != nil {
		config.APIKey, config.APIKeySet = providerproxy.OpenAIOAuthDummyKey, true
		config.FinalizeHeaders = nil // Source chooses the OAuth wrapper in place of buildFetch.
	}
	exchange := &provider.RawHTTP1Exchange{}
	if input.Context.Host.Profile == requestcontext.NativeDirectRelayProfile {
		endpoint := config.BaseURL
		if codex != nil || grok != nil {
			// The wrapper spreads the SDK fetch init. Its proxy options were
			// selected using the original Anthropic baseURL, before remapping.
			// An absent baseURL has no target-aware NO_PROXY bypass.
			endpoint = clientEnv["ANTHROPIC_BASE_URL"]
		} else if input.Context.Host.NativeCodexOAuth != nil && clientEnv["ANTHROPIC_BASE_URL"] == "" && claudeToken == nil {
			return nil, unsupported("first-party Anthropic authentication consumer")
		}
		exchange, err = provider.NativeDirectHTTPExchangeFromEnvironment(endpoint, clientEnv)
		if err != nil {
			return nil, err
		}
	}
	if codex != nil || grok != nil || claudeToken != nil || isHTTPS(input) {
		if deps.TLSState == nil {
			return nil, unsupported("process-owned TLS configuration memo")
		}
		if err := tlsconfig.ValidateOriginalTLSOptions(clientEnv); err != nil {
			return nil, unsupported(err.Error())
		}
		certificates, err := deps.TLSState.Certificates(clientEnv)
		if err != nil {
			return nil, err
		}
		exchange.ResolveVerifiedTLS = func() (*tls.Config, error) {
			return tlsconfig.VerifiedConfigFromCertificates(certificates)
		}
	}
	config.HTTPExchange = exchange
	if deps.Trace != nil {
		binding := tracecapture.Binding{SessionID: request.Host.SessionID, QuerySource: request.Options.QuerySource, Source: "anthropic"}
		if name := clientEnv["CC_HAHA_TRACE_PROVIDER_NAME"]; name != "" {
			var id *string
			if value := clientEnv["CC_HAHA_TRACE_PROVIDER_ID"]; value != "" {
				id = &value
			}
			binding.Provider = &tracecapture.ProviderInfo{ID: id, Name: name, Format: clientEnv["CC_HAHA_TRACE_PROVIDER_FORMAT"]}
		}
		if deps.TraceEnvironment != nil {
			config.TraceBegin = deps.Trace.BindProcess(binding, deps.TraceEnvironment)
		} else {
			config.TraceBegin = deps.Trace.Bind(binding)
		}
	}
	config.OwnerNativeIO = nativeOwner
	if deps.BodyIOAwait != nil {
		config.OwnerBodyIO, err = provider.NewOwnerBodyIO(nativeOwner, deps.BodyIOAwait)
		if err != nil {
			return nil, err
		}
	}
	if deps.OpenIOAwait != nil {
		config.OwnerOpenIO, err = provider.NewOwnerOpenIO(deps.OpenIOAwait)
		if err != nil {
			return nil, err
		}
	}
	if deps.ObserveRequest != nil {
		finalize := config.FinalizeHeaders
		config.FinalizeHeaders = func(endpoint string, headers http.Header) (http.Header, error) {
			resolved := headers.Clone()
			var err error
			if finalize != nil {
				resolved, err = finalize(endpoint, headers)
			}
			if err != nil {
				return nil, err
			}
			prepared := getPrepared()
			if prepared == nil {
				return nil, unsupported("send before independent preparation")
			}
			deps.ObserveRequest(endpoint, resolved.Clone(), append(json.RawMessage{}, prepared.Body...))
			return resolved, nil
		}
	}
	return provider.NewAnthropic(config), nil
}
