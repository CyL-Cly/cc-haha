package clibootstrap

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpcli"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
)

type CLIClaudeAIMCP struct {
	lifetime    context.Context
	refresh     *nativeClaudeMCPRefresh
	Catalog     *mcpcli.ClaudeAICatalog
	PrepareHTTP func(context.Context, protocol.JSONObject, mcpclient.Config) (mcpclient.Config, *http.Client, error)
}

// NewCLIClaudeAIMCP borrows the command's actual owner and credential services.
// It never creates an Agent/ASK or a second token store.
func (m *MainCommand) NewCLIClaudeAIMCP(ctx context.Context, externalHTTP *http.Client) (*CLIClaudeAIMCP, error) {
	value, err := m.newClaudeAIMCP(ctx, externalHTTP, claudeMCPCredentialRun(m.Owner))
	if err == nil {
		m.claudeAIMCP = append(m.claudeAIMCP, value)
	}
	return value, err
}

func (c *CLIClaudeAIMCP) All(ctx context.Context, configurations *mcpconfig.Service) (mcpconfig.Result, error) {
	if configurations.EnterpriseExists() {
		return configurations.AllConfigurations(ctx, protocol.NewJSONObject())
	}
	future := c.Catalog.Begin(c.lifetime)
	return configurations.AllConfigurationsAwait(ctx, func(ctx context.Context) (protocol.JSONObject, error) {
		value := future.Await(ctx)
		return value, context.Cause(ctx)
	})
}

func (c *CLIClaudeAIMCP) Close() {
	if c != nil {
		if c.refresh != nil {
			c.refresh.close()
		}
		if c.Catalog != nil {
			c.Catalog.Close()
		}
	}
}

// Server API consumers bind the existing scoped settings/global/secure stores
// to their actual runtime owner, without constructing a CLI or ASK session.
func (p *NativeProcessSettings) NewClaudeAIMCP(ctx context.Context, secure *securestorage.Store, owner *cooperative.Owner) (*CLIClaudeAIMCP, error) {
	if secure == nil || owner == nil {
		return nil, mainCap("MCP API owned credentials")
	}
	if err := p.ensureSettings(); err != nil {
		return nil, err
	}
	command := &MainCommand{Owner: owner, Native: p.n, Settings: p, Startup: p.startup, Global: p.global, Secure: secure, NonInteractive: true, credentialPlatform: p.platform, credentialDirectory: "/home/claude/.claude/remote", credentialCache: map[string]string{}}
	if p.n.CCRCredentialDirectory != "" {
		command.credentialDirectory = p.n.CCRCredentialDirectory
	}
	return command.newClaudeAIMCP(ctx, nil, claudeMCPCredentialRun(owner))
}

// Credential consumers already run as admitted MCP native work. Read
// completions must re-enter that owner without admitting a second ASK stack.
func claudeMCPCredentialRun(owner *cooperative.Owner) func(context.Context, func(*cooperative.Scope) error) error {
	return func(ctx context.Context, work func(*cooperative.Scope) error) error {
		receipt, err := owner.SubmitIndependent(ctx, cooperative.Read, work)
		if err != nil {
			return err
		}
		return receipt.Wait()
	}
}

func (m *MainCommand) newClaudeAIMCP(ctx context.Context, externalHTTP *http.Client, run func(context.Context, func(*cooperative.Scope) error) error) (*CLIClaudeAIMCP, error) {
	config, e := desktopoauth.ResolveConfig(m.Native.Environment)
	if e != nil {
		return nil, e
	}
	tls, e := tlsconfig.NewState(m.Native.readFile)
	if e != nil {
		return nil, e
	}
	client := externalHTTP
	if client == nil {
		client, e = webfetch.NewHTTPClient(m.Native.Environment, tls)
		if e != nil {
			return nil, e
		}
	}
	cached, loaded := (*openaiauth.Value)(nil), false
	invalidateMemo := func() {
		cached, loaded = nil, false
		m.commandClaudeTokens = nil
		if m.mcpClaudeAuth != nil {
			m.mcpClaudeAuth.claudeTokens, m.mcpClaudeAuth.claudeTokensLoaded = nil, false
		}
	}
	read := func(ctx context.Context, clear bool) (*openaiauth.Value, error) {
		var tokens *openaiauth.Value
		e := run(ctx, func(scope *cooperative.Scope) error {
			if clear {
				invalidateMemo()
				m.Secure.Clear()
			}
			if m.mcpClaudeAuth != nil && !clear && m.Native.Environment["CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR"] == "" {
				var err error
				auth := m.mcpClaudeAuth
				tokens, err = auth.claudeTokensFor(context.WithValue(ctx, nativeOpenAIAuthScopeKey{}, scope), auth.main.nativeOpenAIEnvironment(), envTruthyNative(auth.main.nativeOpenAIEnvironment()["CLAUDE_CODE_SIMPLE"]))
				return err
			}
			if !clear && m.ownsNativeCommandFactory() {
				var err error
				tokens, err = m.nativeCommandClaudeTokens(ctx, scope)
				cached, loaded = tokens, err == nil
				return err
			}
			if !clear && loaded {
				tokens = cached
				return nil
			}
			if envTruthyNative(m.Native.Environment["CLAUDE_CODE_SIMPLE"]) {
				loaded = true
				return nil
			}
			if token := m.Native.Environment["CLAUDE_CODE_OAUTH_TOKEN"]; token != "" {
				tokens = openaiauth.ClaudeEnvironmentToken(token)
			} else if token := m.credentialFromDescriptor("CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR", ".oauth_token"); token != "" {
				tokens = openaiauth.ClaudeEnvironmentToken(token)
			} else {
				var raw json.RawMessage
				var err error
				if clear {
					raw, err = cliAuthPhysical(m, scope, func() (json.RawMessage, error) { return m.Secure.ReadSourceJSONAsync(ctx) })
				} else {
					raw, err = m.Secure.ReadSourceJSON(ctx)
				}
				if err != nil {
					return err
				}
				tokens = openaiauth.ClaudeTokensFromSecure(raw)
			}
			cached, loaded = tokens, true
			if m.ownsNativeCommandFactory() {
				m.commandClaudeTokens = &nativeCommandClaudeTokenMemo{store: m.Secure, tokens: tokens}
			}
			if m.mcpClaudeAuth != nil {
				m.mcpClaudeAuth.claudeTokens, m.mcpClaudeAuth.claudeTokensLoaded = tokens, true
			}
			return nil
		})
		return tokens, e
	}
	producer := &cliauth.OAuthClient{Config: config, HTTP: client, NowMillis: m.Native.NowMillis, Environment: func() map[string]string { return m.Native.Environment }, Settings: func(context.Context) (protocol.JSONObject, error) {
		settings, e := m.Settings.InitialSettings()
		if settings == nil {
			return protocol.NewJSONObject(), e
		}
		return settings.Clone(), e
	}}
	producer.PreviousClaude = func(ctx context.Context) (protocol.JSONObject, protocol.JSONObject, error) {
		global, tokens := protocol.NewJSONObject(), protocol.NewJSONObject()
		e := run(ctx, func(scope *cooperative.Scope) error {
			value, e := m.Global.Get()
			if e != nil {
				return e
			}
			if value != nil {
				global = value.Clone()
			}
			raw, e := m.Secure.ReadSourceJSON(ctx)
			if e != nil {
				return e
			}
			var secure protocol.JSONObject
			_ = secure.UnmarshalJSON(raw)
			if value, ok := secure.Object("claudeAiOauth"); ok {
				tokens = value.Clone()
			}
			return nil
		})
		return global, tokens, e
	}
	producer.UpdateClaudeProfile = func(ctx context.Context, profile protocol.JSONObject) error {
		return run(ctx, func(scope *cooperative.Scope) error { return m.updateCLIRefreshProfile(profile) })
	}
	lifetime, cancel := context.WithCancel(ctx)
	refresh := &nativeClaudeMCPRefresh{command: m, run: run, read: read, client: producer, lifetime: lifetime, cancel: cancel, invalidateMemo: invalidateMemo}
	token := func(ctx context.Context) (*string, error) {
		value, e := read(ctx, false)
		if e != nil || value == nil {
			return nil, e
		}
		s := cliauth.OAuthFieldString(value.Get("accessToken"))
		return &s, nil
	}
	env := map[string]string{}
	for k, v := range m.Native.Environment {
		env[k] = v
	}
	delete(env, "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR")
	catalog := &mcpcli.ClaudeAICatalog{HTTP: client, BaseURL: config.BaseAPIURL, Environment: env, Tokens: func(ctx context.Context) (json.RawMessage, error) {
		value, e := read(ctx, false)
		if e != nil {
			return nil, e
		}
		out := protocol.NewJSONObject()
		if value != nil {
			out.Set("claudeAiOauth", rawMain(value.Object))
		}
		return out.MarshalJSON()
	}}
	base, path := desktopoauth.ResolveMCPProxy(m.Native.Environment)
	return &CLIClaudeAIMCP{lifetime: lifetime, refresh: refresh, Catalog: catalog, PrepareHTTP: func(ctx context.Context, value protocol.JSONObject, parsed mcpclient.Config) (mcpclient.Config, *http.Client, error) {
		if value.String("type") != "claudeai-proxy" {
			return parsed, client, nil
		}
		initial, e := read(ctx, false)
		if e != nil {
			return parsed, nil, e
		}
		if initial == nil {
			return parsed, nil, mainCap("No claude.ai OAuth token found")
		}
		parsed.Type = "http"
		parsed.URL = mcpcli.ClaudeAIProxyAddress(base, path, value.Get("id"))
		clone := *client
		inner := client.Transport
		if inner == nil {
			inner = http.DefaultTransport
		}
		clone.Transport = &mcpcli.ClaudeAIProxyTransport{Inner: inner, CheckAndRefresh: refresh.check, Token: token, Handle401: refresh.handle401}
		return parsed, &clone, nil
	}}, nil
}
