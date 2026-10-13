package clibootstrap

import (
	"context"
	"encoding/json"
	"math"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/contextusage"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
)

// FileReadTool uses countTokensWithAPI, which catches API/invalid-response
// failures as null. Unlike the separate context counter, this path never
// samples a small-fast Messages request. The caller then uses its rough count.
func nativeReadCountRequest(ctx context.Context, client func() (*provider.Anthropic, error), clock provider.AuxiliaryRetry, model, content string, betas []string) (*float64, error) {
	if content == "" {
		n := float64(0)
		return &n, nil
	}
	c, err := client()
	if err != nil {
		return nil, nil
	}
	message := protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoteMain("user")}, protocol.JSONField{Name: "content", Value: quoteMain(content)})
	response, err := c.WithOneSDKRetry(ctx, clock, func(c *provider.Anthropic) (provider.MessageResponse, error) {
		params := contextusage.CountRequest(model, []protocol.JSONObject{message}, []protocol.JSONObject{}, betas)
		bytes, err := params.MarshalJSON()
		if err != nil {
			return provider.MessageResponse{}, err
		}
		bytes, err = protocol.ParseAndStringifyJavaScriptJSON(bytes)
		if err != nil {
			return provider.MessageResponse{}, err
		}
		if err = params.UnmarshalJSON(bytes); err != nil {
			return provider.MessageResponse{}, err
		}
		return c.CountTokens(ctx, params)
	})
	// Runtime destruction must settle the actual native work before owner
	// retirement. Ordinary user interruption supplies no count API signal.
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	if err != nil {
		return nil, nil
	}
	var value protocol.JSONObject
	if value.UnmarshalJSON(response.Raw) != nil {
		return nil, nil
	}
	raw := value.Get("input_tokens")
	if len(raw) == 0 || raw[0] != '-' && (raw[0] < '0' || raw[0] > '9') {
		return nil, nil
	}
	// JSON.parse accepts a valid JSON numeric literal beyond the finite
	// float range as Infinity. It is still typeof number in Source.
	n, parseErr := strconv.ParseFloat(string(raw), 64)
	if parseErr != nil && !math.IsInf(n, 0) {
		return nil, nil
	}
	return &n, nil
}

func (m *MainPrint) nativeReadTokenCount(_ context.Context, scope *cooperative.Scope, content string) (*float64, error) {
	if !scope.BelongsTo(m.Owner) || m.Host == nil || !m.options.Native.NativeProcess {
		return nil, cooperative.ErrRestricted
	}
	model, err := m.nativeReadMainLoopModel(scope)
	if err != nil {
		return nil, err
	}
	env, err := m.nativeProcessEnvironment(scope)
	if err != nil {
		return nil, nil
	}
	if envTruthyNative(env["CLAUDE_CODE_USE_BEDROCK"]) {
		return m.nativeBedrockReadCount(scope, m.nativeReadCountLife.context(), model, content, env)
	}
	settings, err := m.Startup.GetInitialSettings()
	if err != nil {
		return nil, nil
	}
	merged := protocol.NewJSONObject()
	if settings != nil {
		merged = settings.Clone()
	}
	credentials, err := m.nativeReadCountCredentials(scope, env)
	if err != nil {
		return nil, nil
	}
	life := m.nativeReadCountLife.context()
	credentials, err = m.nativeReadCountCloud(scope, life, model, env, credentials)
	if err != nil {
		return nil, nil
	}
	betas, err := nativeReadCountingBetas(model, env, merged, credentials.oauth)
	if err != nil {
		return nil, nil
	}
	redacted, err := m.nativeThinkingSummaryBeta(scope, model)
	if err != nil {
		return nil, nil
	}
	betas = requestcontext.WithThinkingSummaryBeta(betas, redacted)
	session := m.Controller.SessionID()
	work, err := nativeSkillWork(m, scope, func() (*float64, error) {
		return nativeReadCountRequest(life, func() (*provider.Anthropic, error) { return m.nativeCountClient(env, session, credentials) }, provider.AuxiliaryRetry{Now: m.options.Native.Now, Random: nativeMathRandom, Sleep: retrypolicy.SleepAttempt}, model, content, betas)
	})
	if err != nil {
		return nil, err
	}
	return work.Await(scope)
}

// Source countTokensWithAPI reads the CLI module's main-loop model rather
// than the particular Agent query's selected model. In-process Agent owners
// therefore bridge back to that existing root; process workers have no parent.
func (m *MainPrint) nativeReadMainLoopModel(scope *cooperative.Scope) (string, error) {
	if parent := m.nativeHelperParent(); parent != nil {
		return nativeAgentOwnerCall(m, scope, parent, parent.nativeReadMainLoopModel)
	}
	return m.Host.InferenceModel(scope)
}

type nativeReadCountCredentials struct {
	key, token *string
	oauth      bool
	baseURL    string
	vertex     *provider.VertexConfig
	workload   string
}

// Credentials are resolved on the existing owner before the immutable HTTP
// work starts. Source count_tokens shares helper/OAuth auth with ordinary
// requests, without starting an ASK or exporting its credential to tool data.
func (m *MainPrint) nativeReadCountCredentials(scope *cooperative.Scope, env map[string]string) (nativeReadCountCredentials, error) {
	result := nativeReadCountCredentials{workload: m.options.Arguments.Workload}
	if m.openAIAuth != nil {
		subscriber, err := m.openAIAuth.IsClaudeSubscriber(context.Background(), scope, !m.options.Interactive)
		if err != nil {
			return result, err
		}
		if subscriber {
			owned, err := m.openAIAuth.owned(context.Background(), scope)
			if err != nil {
				return result, err
			}
			tokens, err := m.openAIAuth.claudeTokensFor(owned, env, envTruthyNative(env["CLAUDE_CODE_SIMPLE"]))
			if err != nil {
				return result, err
			}
			result.oauth = true
			if tokens != nil {
				var value string
				if err := json.Unmarshal(tokens.Get("accessToken"), &value); err != nil {
					return result, err
				}
				result.token = &value
			}
			return result, nil
		}
	}
	if m.apiKeyHelper != nil {
		binding, err := m.nativeAuthHelperBinding(scope)
		if err != nil {
			return result, err
		}
		resolved, err := binding.Resolve(context.Background(), !m.options.Interactive)
		if err != nil {
			return result, err
		}
		result.key, result.token = resolved.Key, resolved.Token
	}
	if result.key == nil {
		if value, ok := env["ANTHROPIC_API_KEY"]; ok {
			result.key = &value
		}
	}
	if result.token == nil {
		if value := env["ANTHROPIC_AUTH_TOKEN"]; value != "" {
			result.token = &value
		}
	}
	return result, nil
}

func nativeReadCountingBetas(model string, env map[string]string, settings protocol.JSONObject, subscriber bool) ([]string, error) {
	if envTruthyNative(env["CLAUDE_CODE_USE_VERTEX"]) && !envTruthyNative(env["CLAUDE_CODE_USE_BEDROCK"]) {
		// Both producers use getModelBetas. Vertex countTokens admits only
		// these three headers, preserving duplicates and original order.
		params, err := requestcontext.NativeModelValidationRequest(requestcontext.Context{Options: requestcontext.Options{Model: model}, Host: requestcontext.Host{Env: env}}, settings)
		if err != nil {
			return nil, err
		}
		var betas []string
		_ = json.Unmarshal(params.Get("betas"), &betas)
		allowed := []string{}
		for _, beta := range betas {
			if beta == "claude-code-20250219" || beta == "interleaved-thinking-2025-05-14" || beta == "context-management-2025-06-27" {
				allowed = append(allowed, beta)
			}
		}
		return allowed, nil
	}
	countingEnv := env
	if subscriber {
		countingEnv = make(map[string]string, len(env))
		for key, value := range env {
			countingEnv[key] = value
		}
		delete(countingEnv, "ANTHROPIC_BASE_URL")
	}
	betas, err := requestcontext.DirectCountingBetas(model, countingEnv, settings)
	if err != nil {
		return nil, err
	}
	if subscriber {
		at := 0
		if len(betas) > 0 && betas[0] == "claude-code-20250219" {
			at = 1
		}
		betas = append(betas, "")
		copy(betas[at+1:], betas[at:])
		betas[at] = "oauth-2025-04-20"
	}
	return betas, nil
}
