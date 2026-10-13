package clibootstrap

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/contextusage"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

func (m *MainCommand) nativeDoctorCounter(ctx context.Context, scope *cooperative.Scope, binding *ownerasync.NativeBinding, state *bootstrapstate.State, model, fastModel string) (func([]protocol.JSONObject) (*float64, error), error) {
	tls, e := tlsconfig.NewState(m.Native.readFile)
	if e != nil {
		return nil, e
	}
	producer := &NativeMCPToolContext{command: m, platform: m.credentialPlatform, binding: binding, state: state}
	auth, e := producer.ordinaryModelAuth(tls)
	if e != nil {
		return nil, e
	}
	auth.options.Interactive = true
	settings, e := m.Startup.GetInitialSettings()
	if e != nil {
		return nil, e
	}
	env := auth.nativeOpenAIEnvironment()
	betas, e := requestcontext.DirectCountingBetas(model, env, settings.Clone())
	if e != nil {
		return nil, e
	}
	fastBetas, e := requestcontext.DirectCountingBetas(fastModel, env, settings.Clone())
	if e != nil {
		return nil, e
	}
	global, e := m.Global.Get()
	if e != nil {
		return nil, e
	}
	session := state.Snapshot().SessionID
	return func(schemas []protocol.JSONObject) (*float64, error) {
		defer auth.apiKeyHelper.Close(context.Background())
		credentials, e := auth.nativeReadCountCredentials(scope, env)
		if e != nil {
			return nil, nil
		}
		credentials, e = auth.nativeReadCountCloud(scope, ctx, model, env, credentials)
		if e != nil {
			return nil, nil
		}
		work, e := nativeSkillWork(auth, scope, func() (*float64, error) {
			clock := provider.AuxiliaryRetry{Now: m.Native.Now, Random: nativeMathRandom, Sleep: retrypolicy.SleepAttempt}
			client, e := auth.nativeCountClient(env, session, credentials)
			if e != nil {
				return nil, nil
			}
			response, e := client.WithOneSDKRetry(ctx, clock, func(client *provider.Anthropic) (provider.MessageResponse, error) {
				return client.CountTokens(ctx, contextusage.CountRequest(model, nil, schemas, betas))
			})
			if e == nil {
				value := protocol.NewJSONObject()
				if value.UnmarshalJSON(response.Raw) == nil {
					var n float64
					if json.Unmarshal(value.Get("input_tokens"), &n) == nil && string(value.Get("input_tokens")) != "null" {
						return &n, nil
					}
				}
			}
			if ctx.Err() != nil {
				return nil, context.Cause(ctx)
			}
			client, e = auth.nativeCountClient(env, session, credentials)
			if e != nil {
				return nil, nil
			}
			metadata, e := requestcontext.CountingMetadata(env, global.String("userID"), session)
			if e != nil {
				return nil, nil
			}
			response, e = client.WithOneSDKRetry(ctx, clock, func(client *provider.Anthropic) (provider.MessageResponse, error) {
				return client.CreateBetaMessage(ctx, contextusage.HaikuCountRequest(fastModel, nil, schemas, fastBetas, metadata, requestcontext.CountingExtraBody(env)))
			})
			if ctx.Err() != nil {
				return nil, context.Cause(ctx)
			}
			if e != nil {
				return nil, nil
			}
			value := protocol.NewJSONObject()
			if value.UnmarshalJSON(response.Raw) != nil {
				return nil, nil
			}
			usage, ok := value.Object("usage")
			if !ok {
				return nil, nil
			}
			total := 0.0
			for _, key := range []string{"input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"} {
				var n float64
				if json.Unmarshal(usage.Get(key), &n) != nil && key == "input_tokens" {
					return nil, nil
				}
				total += n
			}
			return &total, nil
		})
		if e != nil {
			return nil, e
		}
		return work.Await(scope)
	}, nil
}
