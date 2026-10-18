package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainCommand) AuthLogout(ctx context.Context, scope *cooperative.Scope, openAI bool) (cliauth.Result, error) {
	if scope == nil || !scope.BelongsTo(m.Owner) {
		return cliauth.Result{}, cooperative.ErrRestricted
	}
	if err := scope.Inline(cooperative.Read, func(*cooperative.Scope) error { return ctx.Err() }); err != nil {
		return cliauth.Result{}, err
	}
	if openAI {
		service, err := openaiauth.New(openaiauth.Options{Home: m.Native.Home, CWD: m.Native.CWD, Files: nativeOpenAIAuthFiles{m.Native}, Environment: func() map[string]string { return m.Native.Environment }, SecureRead: m.Secure.ReadSourceJSON, SecureReadAsync: m.Secure.ReadSourceJSONAsync, SecureUpdate: func(ctx context.Context, raw json.RawMessage) (openaiauth.Result, error) {
			r := m.Secure.UpdateSourceJSON(ctx, raw)
			return openaiauth.Result{Success: r.Success}, nil
		}, NowMillis: func() int64 { return int64(m.Native.NowMillis()) }})
		if err != nil {
			return cliauth.Result{}, err
		}
		if !service.Delete(ctx) {
			return cliauth.Result{ExitCode: 1, Stderr: "Failed to log out from OpenAI.\n"}, nil
		}
		return cliauth.Result{Stdout: "Successfully logged out from your OpenAI account.\n"}, nil
	}
	// This command process has no model/telemetry producer or background poller.
	// The original empty flush precedes credential removal; all process caches
	// below belong to this same command and are invalidated before final output.
	_ = m.Secure.RemoveLegacyManagedAPIKey(ctx)
	if err := m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		next := current.Clone()
		next.Delete("primaryApiKey")
		return &next, nil
	}); err != nil {
		return cliauth.Result{ExitCode: 1, Stderr: "Failed to log out.\n"}, nil
	}
	m.clearNativeCommandLegacyAPIKey()
	_ = m.Secure.Delete(ctx)
	m.Secure.Clear()
	m.commandClaudeTokens = nil
	m.Settings.InvalidateSettingsCache()
	if m.remoteManaged != nil {
		if err := m.remoteManaged.clear(scope); err != nil {
			return cliauth.Result{}, err
		}
	} else {
		remote := filepath.Join(m.Native.ConfigHomeDirectory(), "remote-settings.json")
		if err := m.Native.check("logout remote settings cache", remote); err != nil {
			return cliauth.Result{}, err
		}
		_ = os.Remove(remote)
	}
	if err := m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		next := current.Clone()
		next.Delete("oauthAccount")
		return &next, nil
	}); err != nil {
		return cliauth.Result{ExitCode: 1, Stderr: "Failed to log out.\n"}, nil
	}
	return cliauth.Result{Stdout: "Successfully logged out from your Anthropic account.\n"}, nil
}

func (m *MainCommand) AuthStatus(ctx context.Context, scope *cooperative.Scope, openAI, text bool) (cliauth.Result, error) {
	if scope == nil || !scope.BelongsTo(m.Owner) {
		return cliauth.Result{}, cooperative.ErrRestricted
	}
	if err := scope.Inline(cooperative.Read, func(*cooperative.Scope) error { return ctx.Err() }); err != nil {
		return cliauth.Result{}, err
	}
	env := m.Native.Environment
	storage := cliauth.NewOpenAIStorage(cliauth.StorageOptions{Home: m.Native.Home, Environment: func(k string) string {
		if k == "OPENAI_CODEX_OAUTH_FILE" {
			return m.OpenAIStoragePath()
		}
		return env[k]
	}, ReadFile: m.Native.readFile, Exists: m.CredentialFileExists, ReadSecure: func() (json.RawMessage, error) { return m.Secure.ReadSourceJSON(ctx) }})
	if openAI {
		return cliauth.OpenAIStatus(storage, text), nil
	}
	input, tokens, keyPresent, enabled, err := m.cliAuthIdentity(ctx, scope, m.NonInteractive)
	if err != nil {
		return cliauth.Result{}, err
	}
	input.OpenAITokens = storage.Tokens()
	provider := input.APIProvider
	subscriber, err := openaiauth.ClaudeSubscriber(enabled, tokens)
	if err != nil {
		return cliauth.Result{}, err
	}
	if text {
		input.Properties = cliauth.AccountProperties(input, keyPresent, subscriber, env["IS_DEMO"] != "")
		input.Properties = append(input.Properties, authProviderProperties(env, provider)...)
		// Original getMTLSConfig reads native files and reports only successfully
		// read, truthy contents. Missing files do not invent configured certs.
		for _, entry := range []struct{ key, label string }{{"CLAUDE_CODE_CLIENT_CERT", "mTLS client cert"}, {"CLAUDE_CODE_CLIENT_KEY", "mTLS client key"}} {
			if path := env[entry.key]; path != "" {
				if !filepath.IsAbs(path) {
					path = filepath.Join(m.Native.CWD, path)
				}
				data, e := m.Native.readFile(path)
				if e != nil && !errors.Is(e, fs.ErrNotExist) {
					continue
				}
				if len(data) > 0 {
					input.Properties = append(input.Properties, cliauth.Property{Label: entry.label, Value: quoteMain(env[entry.key])})
				}
			}
		}
	}
	return cliauth.GeneralStatus(input, text), nil
}

func authProviderProperties(env map[string]string, provider string) []cliauth.Property {
	list := []cliauth.Property{}
	add := func(label, value string) {
		if value != "" {
			list = append(list, cliauth.Property{Label: label, Value: quoteMain(value)})
		}
	}
	switch provider {
	case "firstParty":
		add("Anthropic base URL", env["ANTHROPIC_BASE_URL"])
	case "bedrock":
		add("API provider", "AWS Bedrock")
		add("Bedrock base URL", env["BEDROCK_BASE_URL"])
		region := env["AWS_REGION"]
		if region == "" {
			region = env["AWS_DEFAULT_REGION"]
		}
		if region == "" {
			region = "us-east-1"
		}
		add("AWS region", region)
		if envTruthyNative(env["CLAUDE_CODE_SKIP_BEDROCK_AUTH"]) {
			add("", "AWS auth skipped")
		}
	case "vertex":
		add("API provider", "Google Vertex AI")
		add("Vertex base URL", env["VERTEX_BASE_URL"])
		add("GCP project", env["ANTHROPIC_VERTEX_PROJECT_ID"])
		region := env["CLOUD_ML_REGION"]
		if region == "" {
			region = "us-east5"
		}
		add("Default region", region)
		if envTruthyNative(env["CLAUDE_CODE_SKIP_VERTEX_AUTH"]) {
			add("", "GCP auth skipped")
		}
	case "foundry":
		add("API provider", "Microsoft Foundry")
		add("Microsoft Foundry base URL", env["ANTHROPIC_FOUNDRY_BASE_URL"])
		add("Microsoft Foundry resource", env["ANTHROPIC_FOUNDRY_RESOURCE"])
		if envTruthyNative(env["CLAUDE_CODE_SKIP_FOUNDRY_AUTH"]) {
			add("", "Microsoft Foundry auth skipped")
		}
	}
	for _, key := range []string{"https_proxy", "HTTPS_PROXY", "http_proxy", "HTTP_PROXY"} {
		if env[key] != "" {
			add("Proxy", env[key])
			break
		}
	}
	add("Additional CA cert(s)", env["NODE_EXTRA_CA_CERTS"])
	return list
}
