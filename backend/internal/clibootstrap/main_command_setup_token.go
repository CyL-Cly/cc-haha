package clibootstrap

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
)

type SetupTokenOptions struct {
	HTTP                *http.Client
	Config              *desktopoauth.Config
	Browser             func(context.Context, string) error
	Input               io.Reader
	TerminalEnvironment []string
}

func (m *MainCommand) SetupToken(ctx context.Context, scope *cooperative.Scope, o SetupTokenOptions) (cliauth.Result, error) {
	if scope == nil || !scope.BelongsTo(m.Owner) || !m.Native.NativeProcess {
		return cliauth.Result{}, cooperative.ErrRestricted
	}
	config, e := desktopoauth.ResolveConfig(m.Native.Environment)
	if e != nil {
		return cliauth.Result{}, e
	}
	if o.Config != nil {
		config = *o.Config
	}
	tls, e := tlsconfig.NewState(m.Native.readFile)
	if e != nil {
		return cliauth.Result{}, e
	}
	client := o.HTTP
	if client == nil {
		client, e = webfetch.NewHTTPClient(m.Native.Environment, tls)
		if e != nil {
			return cliauth.Result{}, e
		}
	}
	settings, e := m.Startup.GetInitialSettings()
	if e != nil {
		return cliauth.Result{}, e
	}
	merged := protocol.NewJSONObject()
	if settings != nil {
		merged = settings.Clone()
	}
	warning, e := m.setupTokenAuthWarning(ctx, merged)
	if e != nil {
		return cliauth.Result{}, e
	}
	browser := o.Browser
	if browser == nil {
		browser = m.authLoginBrowser
	}
	clipboard := cliauth.NewSetupTokenClipboard(ctx, m.credentialPlatform, m.Native.CWD, m.Native.Environment)
	defer clipboard.Close()
	producer := &cliauth.OAuthClient{Config: config, HTTP: client, NowMillis: m.Native.NowMillis}
	data, e := scope.AwaitBytes(cooperative.NetworkOpen, func(worker context.Context) ([]byte, error) {
		result, e := cliauth.RunSetupTokenTerminal(worker, cliauth.SetupTokenRuntime{Client: producer, Settings: merged, Browser: browser}, cliauth.SetupTokenTerminal{Input: o.Input, Output: m.Output, Environment: o.TerminalEnvironment, AuthWarning: warning, ForcedMethod: merged.String("forceLoginMethod"), Clipboard: clipboard.Copy})
		if e != nil {
			return nil, e
		}
		return json.Marshal(result)
	})
	if e != nil {
		return cliauth.Result{}, e
	}
	var result cliauth.Result
	e = json.Unmarshal(data, &result)
	return result, e
}

// Only the API-key source relevant to isAnthropicAuthEnabled is observed.
// No OAuth token installation, scope validation, helper execution or logout.
func (m *MainCommand) setupTokenAuthWarning(ctx context.Context, settings protocol.JSONObject) (bool, error) {
	env := m.Native.Environment
	bare := envTruthyNative(env["CLAUDE_CODE_SIMPLE"])
	helper := settings.Get("apiKeyHelper")
	if bare {
		return true, nil
	}
	if env["ANTHROPIC_UNIX_SOCKET"] != "" {
		return env["CLAUDE_CODE_OAUTH_TOKEN"] == "", nil
	}
	source := "none"
	key := env["ANTHROPIC_API_KEY"]
	if env["USER_TYPE"] == "ant" && envTruthyNative(env["COO_RUNNING_ON_HOMESPACE"]) {
		key = ""
	}
	if envTruthyNative(env["CI"]) || env["NODE_ENV"] == "test" {
		if m.credentialFromDescriptor("CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR", ".api_key") != "" || key != "" {
			source = "ANTHROPIC_API_KEY"
		}
	} else {
		approved := false
		if key != "" {
			global, e := m.Global.Get()
			if e == nil && global != nil {
				approved, e = openaiauth.IsCustomAPIKeyApproved(*global, quoteMain(key))
				if e != nil {
					approved = false
				}
			}
		}
		if approved || m.credentialFromDescriptor("CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR", ".api_key") != "" {
			source = "ANTHROPIC_API_KEY"
		} else if openaiauth.AuthTruthy(helper) {
			source = "apiKeyHelper"
		} else {
			// The original source lookup may read its existing managed key; it never
			// creates one. Preserve the same private/native credential boundary.
			_, e := m.Secure.ReadLegacyManagedAPIKey(ctx)
			if e != nil && ctx.Err() != nil {
				return false, e
			}
		}
	}
	enabled := openaiauth.AnthropicAuthEnabled(openaiauth.AnthropicAuthInput{Bare: bare, Environment: env, APIKeySource: source, APIKeyHelper: helper})
	return !enabled, nil
}
