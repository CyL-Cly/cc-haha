package application

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/autoquestion"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/networksettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerproxytransport"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/voice"
)

func newDesktopAutoQuestion(o ServerOptions, settings *settings.Service, providers *providerconfig.Service, oauth *desktopoauth.Service, proxy *providerproxytransport.Service) (*autoquestion.Service, error) {
	decider, e := newDesktopAutoQuestionDecider(o, settings, providers, oauth, proxy)
	if e != nil {
		return nil, e
	}
	read := func(ctx context.Context) (json.RawMessage, error) {
		v, e := settings.GetUserSettings(ctx)
		return v["autoQuestion"], e
	}
	return autoquestion.New(autoquestion.Options{ReadSettings: read, Decider: decider, Log: func(reason string, e error) {
		if o.Stderr != nil {
			fmt.Fprintf(o.Stderr, "[ConversationService] Automatic answer left pending: reason=%s error=%v\n", reason, e)
		}
	}})
}

func newDesktopAutoQuestionDecider(o ServerOptions, settings *settings.Service, providers *providerconfig.Service, oauth *desktopoauth.Service, proxy *providerproxytransport.Service) (autoquestion.Decider, error) {
	tls, e := tlsconfig.NewState(os.ReadFile)
	if e != nil {
		return autoquestion.Decider{}, e
	}
	resolve := func(ctx context.Context, id *string, sessionID string) (*autoquestion.Target, error) {
		target := &autoquestion.Target{Headers: http.Header{"Content-Type": []string{"application/json"}, "Anthropic-Version": []string{"2023-06-01"}}}
		if id == nil {
			model, e := oauth.ResolveRuntimeModel(ctx, raw("haiku"))
			if e != nil || model == nil {
				return nil, e
			}
			token, e := oauth.EnsureFreshAccessToken(ctx)
			if e != nil || token == nil || *token == "" {
				return nil, e
			}
			config, e := desktopoauth.ResolveConfig(o.Environment)
			if e != nil {
				return nil, e
			}
			target.Model = *model
			target.URL = provider.NormalizeAnthropicBaseURL(config.BaseAPIURL) + "/v1/messages"
			target.Headers.Set("Anthropic-Beta", "oauth-2025-04-20")
			target.Headers.Set("Authorization", "Bearer "+*token)
		} else {
			p, e := providers.Provider(ctx, *id)
			if e != nil {
				return nil, e
			}
			v := obj(raw(p))
			models := obj(v.Get("models"))
			target.Model = models.String("haiku")
			if target.Model == "" {
				target.Model = models.String("main")
			}
			if target.Model == "" {
				return nil, nil
			}
			if *id == "openai-official" {
				if oauth == nil || oauth.OpenAI() == nil {
					return nil, &clibootstrap.MainCapabilityError{Boundary: "automatic question official OpenAI OAuth credential consumer"}
				}
				tokens, e := oauth.OpenAI().EnsureFreshTokens(ctx)
				if e != nil || tokens == nil || tokens.Record().String("accessToken") == "" {
					return nil, e
				}
				target.URL = providerproxy.OpenAICodexEndpoint
				target.Headers = http.Header{"Content-Type": {"application/json"}, "Authorization": {"Bearer " + tokens.Record().String("accessToken")}}
				if account := tokens.Record().String("accountId"); account != "" {
					target.Headers.Set("ChatGPT-Account-Id", account)
				}
				target.NoThinkingFallback, target.FirstTextOnly = true, true
				mapped := providerproxy.ResolveOpenAICodexModel(target.Model, o.Environment)
				target.Send = func(ctx context.Context, data json.RawMessage) (*http.Response, error) {
					body := obj(data).Clone()
					body.Set("model", raw(mapped))
					body.Set("stream", raw(true))
					body.Set("thinking", []byte(`{"type":"disabled"}`))
					upstream, e := providerproxy.AnthropicToResponses(body, protocol.NewJSONObject())
					if e != nil {
						return nil, e
					}
					upstream.Set("stream", raw(true))
					upstream.Set("max_output_tokens", raw(512))
					return sendDesktopAutoQuestion(ctx, target, raw(upstream), mapped)
				}
			} else if *id == "grok-official" {
				if oauth == nil || oauth.Grok() == nil {
					return nil, &clibootstrap.MainCapabilityError{Boundary: "automatic question official Grok OAuth credential consumer"}
				}
				tokens, e := oauth.Grok().EnsureFreshTokens(ctx)
				if e != nil || tokens == nil || tokens.Record().String("accessToken") == "" {
					return nil, e
				}
				platform := runtime.GOOS
				if platform == "windows" {
					platform = "win32"
				}
				arch := runtime.GOARCH
				if arch == "amd64" {
					arch = "x64"
				}
				target.URL = providerproxy.GrokCLIBaseURL + "/responses"
				target.Headers, e = providerproxy.BuildGrokIdentityHeaders(tokens.Record().Get("accessToken"), platform, arch)
				if e != nil {
					return nil, e
				}
				target.Headers.Set("x-grok-model-override", target.Model)
				target.NoThinkingFallback, target.FirstTextOnly = true, true
				target.Send = func(ctx context.Context, data json.RawMessage) (*http.Response, error) {
					body := obj(data).Clone()
					body.Set("stream", raw(true))
					body.Set("thinking", []byte(`{"type":"disabled"}`))
					upstream, e := providerproxy.AnthropicToResponses(body, protocol.NewJSONObject())
					if e != nil {
						return nil, e
					}
					upstream.Set("model", raw(target.Model))
					upstream.Set("stream", raw(true))
					upstream.Set("max_output_tokens", raw(512))
					return sendDesktopAutoQuestion(ctx, target, raw(upstream), target.Model)
				}
			} else {
				base := v.String("baseUrl")
				if base == "" {
					return nil, nil
				}
				config, e := providers.ProviderForProxy(ctx, *id)
				if e != nil {
					return nil, e
				}
				if config != nil && (config.APIFormat != "anthropic" || !config.SupportsNestedToolResultMedia) {
					if proxy == nil {
						return nil, &clibootstrap.MainCapabilityError{Boundary: "automatic question protocol/media proxy consumer"}
					}
					// Source invokes the same scoped handler in-process, before the
					// direct-provider API-key check. No local bearer or dummy key is
					// forwarded as upstream credentials.
					target.URL = "http://127.0.0.1/proxy/providers/" + url.PathEscape(*id) + "/v1/messages"
					target.Send = func(ctx context.Context, body json.RawMessage) (*http.Response, error) {
						req, e := http.NewRequestWithContext(ctx, http.MethodPost, target.URL, strings.NewReader(string(body)))
						if e != nil {
							return nil, e
						}
						req.Header.Set("Content-Type", "application/json")
						if sessionID != "" {
							req.Header.Set("x-claude-code-session-id", sessionID)
						}
						return proxy.Handle(req)
					}
					return target, nil
				}
				env, e := providerconfig.BuildManagedEnv(p)
				if e != nil {
					return nil, e
				}
				// Source tests key availability before applying even dummy auth.
				key := v.String("apiKey")
				if key == "" {
					var presets []json.RawMessage
					_ = json.Unmarshal(providerconfig.Presets(), &presets)
					for _, data := range presets {
						preset := obj(data)
						if preset.String("id") == v.String("presetId") {
							defaults := obj(preset.Get("defaultEnv"))
							key = defaults.String("ANTHROPIC_AUTH_TOKEN")
							if key == "" {
								key = defaults.String("ANTHROPIC_API_KEY")
							}
							break
						}
					}
				}
				if key == "" {
					return nil, nil
				}
				if key := env["ANTHROPIC_API_KEY"]; key != "" {
					target.Headers.Set("X-Api-Key", key)
				}
				if token := env["ANTHROPIC_AUTH_TOKEN"]; token != "" {
					target.Headers.Set("Authorization", "Bearer "+token)
				}
				target.URL = provider.NormalizeAnthropicBaseURL(strings.TrimRight(base, "/")) + "/v1/messages"
			}
		}
		user, e := settings.GetUserSettings(ctx)
		if e != nil {
			return nil, e
		}
		env := map[string]string{}
		for k, v := range o.Environment {
			env[k] = v
		}
		network, e := networksettings.BuildEnvironment(networksettings.Normalize(raw(user)), o.Environment)
		if e != nil {
			return nil, e
		}
		for k, v := range network {
			env[k] = v
		}
		target.Client, e = voice.NewHTTPClient(env, tls)
		if e == nil {
			target.Close = func() { target.Client.CloseIdleConnections() }
			if target.Send == nil {
				target.Send = func(ctx context.Context, body json.RawMessage) (*http.Response, error) {
					return sendDesktopAutoQuestion(ctx, target, body, "")
				}
			}
		}
		return target, e
	}
	return autoquestion.Decider{Resolve: resolve}, nil
}
