package clibootstrap

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/contextusage"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/tracecapture"
)

type nativeTokenCountLifetime struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func (m *MainPrint) nativeTokenCounter(ctx context.Context, scope *cooperative.Scope, model string) (contextusage.Counter, error) {
	if !scope.BelongsTo(m.Owner) || !m.options.Native.NativeProcess || m.tls == nil {
		return nil, mainCap("native count-token client lifetime")
	}
	if m.nativeTokenCountLife == nil {
		life, cancel := context.WithCancel(context.Background())
		if _, err := m.Cleanup.Register(scope, runtimecleanup.NewCallback(func(*cooperative.Scope) (*runtimecleanup.Future, error) { cancel(); return nil, nil })); err != nil {
			cancel()
			return nil, err
		}
		m.nativeTokenCountLife = &nativeTokenCountLifetime{life, cancel}
	}
	env := map[string]string{}
	for _, field := range m.environment.Snapshot().OwnProperties() {
		var name, value string
		_ = json.Unmarshal(field.NameJSON, &name)
		_ = json.Unmarshal(field.Value, &value)
		env[name] = value
	}
	settings, err := m.Startup.GetInitialSettings()
	if err != nil {
		return nil, err
	}
	selector, err := modelselection.NewDirectCredentials(modelselection.Options{Environment: env, Settings: settings.Clone()})
	if err != nil {
		return nil, err
	}
	betas, err := requestcontext.DirectCountingBetas(model, env, settings.Clone())
	if err != nil {
		return nil, err
	}
	fastModel := selector.SmallFastModel()
	fastBetas, err := requestcontext.DirectCountingBetas(fastModel, env, settings.Clone())
	if err != nil {
		return nil, err
	}
	global, err := m.Global.Get()
	if err != nil {
		return nil, err
	}
	session := m.Controller.SessionID()
	device := global.String("userID")
	life := m.nativeTokenCountLife.ctx
	return func(messages, tools []protocol.JSONObject) (*float64, error) {
		future, err := nativeSkillWork(m, scope, func() (*float64, error) {
			callCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			stop := context.AfterFunc(life, cancel)
			defer stop()
			client, err := m.nativeCountClient(env, session)
			if err != nil {
				return nil, err
			}
			clock := provider.AuxiliaryRetry{Now: m.options.Native.Now, Random: nativeMathRandom, Sleep: retrypolicy.SleepAttempt}
			response, err := client.WithOneSDKRetry(callCtx, clock, func(c *provider.Anthropic) (provider.MessageResponse, error) {
				return c.CountTokens(callCtx, contextusage.CountRequest(model, messages, tools, betas))
			})
			if err == nil {
				var value protocol.JSONObject
				if value.UnmarshalJSON(response.Raw) == nil {
					var n float64
					if json.Unmarshal(value.Get("input_tokens"), &n) == nil && string(value.Get("input_tokens")) != "null" {
						return &n, nil
					}
				}
			}
			if callCtx.Err() != nil {
				return nil, callCtx.Err()
			}
			// Source reconstructs its fallback client with the small-fast model.
			client, err = m.nativeCountClient(env, session)
			if err != nil {
				return nil, err
			}
			metadata, err := requestcontext.CountingMetadata(env, device, session)
			if err != nil {
				return nil, nil
			}
			extra := requestcontext.CountingExtraBody(env)
			response, err = client.WithOneSDKRetry(callCtx, clock, func(c *provider.Anthropic) (provider.MessageResponse, error) {
				return c.CreateBetaMessage(callCtx, contextusage.HaikuCountRequest(fastModel, messages, tools, fastBetas, metadata, extra))
			})
			if callCtx.Err() != nil {
				return nil, callCtx.Err()
			}
			if err != nil {
				return nil, nil
			}
			var value protocol.JSONObject
			if value.UnmarshalJSON(response.Raw) != nil {
				return nil, nil
			}
			var usage protocol.JSONObject
			if usage.UnmarshalJSON(value.Get("usage")) != nil {
				return nil, nil
			}
			var total float64
			for _, key := range []string{"input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"} {
				var n float64
				if json.Unmarshal(usage.Get(key), &n) != nil && key == "input_tokens" {
					return nil, nil
				}
				total += n
			}
			return &total, nil
		})
		if err != nil {
			return nil, err
		}
		return future.Await(scope)
	}, nil
}

func (m *MainPrint) nativeCountClient(env map[string]string, session string) (*provider.Anthropic, error) {
	var key *string
	if value, exists := env["ANTHROPIC_API_KEY"]; exists {
		copy := value
		key = &copy
	}
	config := provider.ResolveDirectClientOptions(provider.DirectClientInput{Env: env, FallbackAPIKey: key, SessionID: session, Runtime: provider.SourceRuntime{Platform: m.options.Platform, Arch: "arm64", Version: "v24.3.0"}, RequestID: uuidMain}).StreamConfig()
	exchange, err := provider.NativeDirectHTTPExchangeFromEnvironment(config.BaseURL, env)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(config.BaseURL, "https:") {
		if err = tlsconfig.ValidateOriginalTLSOptions(env); err != nil {
			return nil, err
		}
		certs, err := m.tls.Certificates(env)
		if err != nil {
			return nil, err
		}
		exchange.ResolveVerifiedTLS = func() (*tls.Config, error) { return tlsconfig.VerifiedConfigFromCertificates(certs) }
	}
	config.HTTPExchange = exchange
	if m.options.Trace != nil {
		begin := m.options.Trace.BindProcess(tracecapture.Binding{SessionID: session, QuerySource: "count_tokens", Source: "anthropic"}, env)
		config.TraceBegin = func(input tracecapture.FetchInput) tracecapture.FetchCapture {
			// Source's count/fallback call supplies no AbortSignal. Internal Go
			// request cleanup must not turn a completed response into a user abort.
			// Actual fetch/body failures still arrive through the physical clone.
			input.Context = context.WithoutCancel(input.Context)
			return begin(input)
		}
	}
	return provider.NewAnthropic(config), nil
}
