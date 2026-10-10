package clibootstrap

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providernative"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

func (m *MainPrint) nativeOpenAIEnvironment() map[string]string {
	env := map[string]string{}
	for _, field := range m.environment.Snapshot().OwnProperties() {
		var name, value string
		_ = json.Unmarshal(field.NameJSON, &name)
		_ = json.Unmarshal(field.Value, &value)
		env[name] = value
	}
	return env
}

func (m *MainPrint) nativeAPIKeySource(scope *cooperative.Scope) (string, error) {
	if parent := m.nativeHelperParent(); parent != nil {
		return nativeAgentOwnerCall(m, scope, parent, parent.nativeAPIKeySource)
	}
	if m.openAIAuth == nil {
		return "ANTHROPIC_API_KEY", nil
	}
	ctx, err := m.openAIAuth.owned(context.Background(), scope)
	if err != nil {
		return "", err
	}
	env := m.nativeOpenAIEnvironment()
	bare := envTruthyNative(env["CLAUDE_CODE_SIMPLE"])
	if _, err = m.openAIAuth.authDependencies(env, bare); err != nil {
		return "", err
	}
	if env["CLAUDE_CODE_ENTRYPOINT"] == "claude-vscode" {
		return "", mainCap("actual VSCode bootstrap clientType auth preference")
	}
	return m.openAIAuth.apiKeySource(ctx, env, bare, !m.options.Interactive)
}

// The CLI helper calls refreshOpenAITokens with its default options. It has
// neither the SDK request's signal nor a new desktop token-request timeout.
// Its fetch, response bytes and persistence resume on this same original owner.
func (m *MainPrint) nativeOpenAIRefresh(ctx context.Context, scope *cooperative.Scope, token json.RawMessage) (protocol.JSONObject, error) {
	plan, err := desktopoauth.OpenAIRefreshRequest(token)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	env := m.nativeOpenAIEnvironment()
	if err := tlsconfig.ValidateOriginalTLSOptions(env); err != nil {
		return protocol.JSONObject{}, err
	}
	certificates, err := m.tls.Certificates(env)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	// Generic refresh has no targetUrl; Source does not apply its target-aware
	// NO_PROXY bypass or Anthropic-only unix socket to this call.
	routing := desktopoauth.OpenAIRefreshFetchOptions(env, nil, false)
	exchange := &provider.RawHTTP1Exchange{ProxyURL: routing.Proxy}
	exchange.ResolveVerifiedTLS = func() (*tls.Config, error) {
		return tlsconfig.VerifiedConfigFromCertificates(certificates)
	}
	driver, err := providernative.New(m.Owner, scope, nil)
	if err != nil {
		return protocol.JSONObject{}, nativeOpenAIAuthOwnerFailure(err)
	}
	bodyIO, err := provider.NewOwnerBodyIO(driver, func(_ provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
		return scope.AwaitBytes(cooperative.StreamNext, work)
	})
	if err != nil {
		return protocol.JSONObject{}, nativeOpenAIAuthOwnerFailure(err)
	}
	request, err := http.NewRequestWithContext(context.WithoutCancel(ctx), plan.Method, plan.URL, strings.NewReader(plan.Body))
	if err != nil {
		return protocol.JSONObject{}, err
	}
	for _, header := range plan.Headers {
		request.Header.Set(header[0], header[1])
	}
	response, err := driver.OpenFetch(request, exchange)
	if err != nil {
		if provider.IsOwnerWaitFailure(err) {
			return protocol.JSONObject{}, &NativeOpenAIAuthAdministrativeError{err}
		}
		return protocol.JSONObject{}, err
	}
	defer response.Response.Body.Close()
	raw, readErr := bodyIO.ReadFetchBody(response.Response.Body)
	if provider.IsOwnerWaitFailure(readErr) {
		return protocol.JSONObject{}, &NativeOpenAIAuthAdministrativeError{readErr}
	}
	raw, err = desktopoauth.DecodeOpenAITokenResponse(response.Response.StatusCode, raw, readErr)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	var object protocol.JSONObject
	// Source can return any JSON root. JSONObject retains the original root;
	// the storage producer performs its original property lookups afterward.
	err = object.UnmarshalJSON(raw)
	return object, err
}

func (m *MainPrint) nativeOpenAIConfig(scope *cooperative.Scope) *provider.OpenAICodexConfig {
	if m.openAIAuth == nil {
		return nil
	}
	config := &provider.OpenAICodexConfig{Env: m.nativeOpenAIEnvironment(), NowMillis: func() int64 { return int64(m.options.Native.NowMillis()) }}
	if m.options.AgentID != "" {
		config.AgentID = quoteMain(m.options.AgentID)
	}
	config.HasAuth = func(ctx context.Context) (bool, error) {
		return m.openAIAuth.HasAuth(context.WithoutCancel(ctx), scope)
	}
	config.IsClaudeSubscriber = func(ctx context.Context, nonInteractive bool) (bool, error) {
		return m.openAIAuth.IsClaudeSubscriber(context.WithoutCancel(ctx), scope, nonInteractive)
	}
	config.FreshTokens = func(ctx context.Context) (protocol.JSONObject, error) {
		value, err := m.openAIAuth.EnsureFresh(context.WithoutCancel(ctx), scope)
		if err != nil || value == nil {
			return protocol.JSONObject{}, err
		}
		return value.Object, nil
	}
	config.Encode = func(plain []byte) ([]byte, error) {
		return scope.AwaitBytes(cooperative.SDKOutput, func(context.Context) ([]byte, error) { return providerproxy.EncodeOpenAIZstd(plain) })
	}
	return config
}
