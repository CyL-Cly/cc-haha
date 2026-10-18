package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainCommand) installCLIClaudeTokens(ctx context.Context, scope *cooperative.Scope, client *cliauth.OAuthClient, tokens protocol.JSONObject, version string) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	result, e := m.AuthLogout(ctx, scope, false)
	if e != nil {
		return e
	}
	if result.ExitCode != 0 {
		return errors.New("Failed to log out.")
	}
	profile, has := tokens.Object("profile")
	if !has {
		fetched, e := cliAuthPhysical(m, scope, func() (*protocol.JSONObject, error) { return client.Profile(ctx, tokens.String("accessToken")), nil })
		if e != nil {
			return e
		}
		if fetched != nil {
			profile = *fetched
			has = true
		}
	}
	account := protocol.NewJSONObject()
	if has {
		a, _ := profile.Object("account")
		org, _ := profile.Object("organization")
		for _, pair := range [][2]string{{"accountUuid", "uuid"}, {"emailAddress", "email"}, {"accountCreatedAt", "created_at"}} {
			account.Set(pair[0], a.Get(pair[1]))
		}
		account.Set("organizationUuid", org.Get("uuid"))
		if a.String("display_name") != "" {
			account.Set("displayName", a.Get("display_name"))
		}
		for _, pair := range [][2]string{{"hasExtraUsageEnabled", "has_extra_usage_enabled"}, {"billingType", "billing_type"}, {"subscriptionCreatedAt", "subscription_created_at"}} {
			if v := org.Get(pair[1]); v != nil && string(v) != "null" {
				account.Set(pair[0], v)
			}
		}
	} else if a, ok := tokens.Object("tokenAccount"); ok {
		account.Set("accountUuid", a.Get("uuid"))
		account.Set("emailAddress", a.Get("emailAddress"))
		account.Set("organizationUuid", a.Get("organizationUuid"))
	}
	if len(account.OwnProperties()) > 0 {
		if e = m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			next := current.Clone()
			next.Set("oauthAccount", rawMain(account))
			return &next, nil
		}); e != nil {
			return e
		}
	}
	var scopes []string
	_ = json.Unmarshal(tokens.Get("scopes"), &scopes)
	inference := false
	for _, scope := range scopes {
		inference = inference || scope == "user:inference"
	}
	if inference && openaiauth.AuthTruthy(tokens.Get("refreshToken")) && openaiauth.AuthTruthy(tokens.Get("expiresAt")) {
		stored := protocol.NewJSONObject()
		for _, name := range []string{"accessToken", "refreshToken", "expiresAt", "scopes", "subscriptionType", "rateLimitTier"} {
			value := tokens.Get(name)
			if (name == "subscriptionType" || name == "rateLimitTier") && value == nil {
				value = []byte(`null`)
			}
			stored.Set(name, value)
		}
		_, _ = m.Secure.Change(ctx, func(value protocol.JSONObject) error { value.Set("claudeAiOauth", rawMain(stored)); return nil })
		m.Secure.Clear()
		m.commandClaudeTokens = nil
	}
	roles, e := cliAuthPhysical(m, scope, func() (protocol.JSONObject, error) { return client.FetchRoles(ctx, tokens.String("accessToken")) })
	if e == nil {
		_ = m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			next := current.Clone()
			account, ok := next.Object("oauthAccount")
			if ok {
				for _, pair := range [][2]string{{"organizationRole", "organization_role"}, {"workspaceRole", "workspace_role"}, {"organizationName", "organization_name"}} {
					account.Set(pair[0], roles.Get(pair[1]))
				}
				next.Set("oauthAccount", rawMain(account))
			}
			return &next, nil
		})
	}
	if inference {
		global, e := m.Global.Get()
		if e == nil && global.Get("claudeCodeFirstTokenDate") == nil {
			identity, currentToken, keyPresent, enabled, identityError := m.cliAuthIdentity(ctx, scope, true)
			subscriber, subscriberError := openaiauth.ClaudeSubscriber(enabled, currentToken)
			headers := [][2]string{}
			if identityError == nil && subscriberError == nil {
				if subscriber && currentToken != nil {
					headers = append(headers, [2]string{"Authorization", "Bearer " + currentTokenString(currentToken, "accessToken")}, [2]string{"anthropic-beta", "oauth-2025-04-20"})
				} else if keyPresent {
					key := ""
					if identity.APIKeySource == "ANTHROPIC_API_KEY" {
						key = m.credentialFromDescriptor("CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR", ".api_key")
						if key == "" {
							key = m.Native.Environment["ANTHROPIC_API_KEY"]
						}
					}
					if identity.APIKeySource == "/login managed key" {
						key, _, _ = m.nativeCommandLegacyAPIKey(ctx, scope)
						if key == "" {
							key = global.String("primaryApiKey")
						}
					}
					if key != "" {
						headers = append(headers, [2]string{"x-api-key", key})
					}
				}
			}
			if len(headers) > 0 {
				date, e := cliAuthPhysical(m, scope, func() (json.RawMessage, error) { return client.FirstTokenDateHeaders(ctx, headers, version) })
				if e == nil && cliauth.ValidFirstTokenDate(date) {
					_ = m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
						next := current.Clone()
						next.Set("claudeCodeFirstTokenDate", date)
						return &next, nil
					})
				}
			}
		}
	} else {
		key, e := cliAuthPhysical(m, scope, func() (string, error) { return client.CreateAPIKey(ctx, tokens.String("accessToken")) })
		if e != nil {
			return e
		}
		if key == "" {
			return errors.New("Unable to create API key. The server accepted the request but did not return a key.")
		}
		if e = m.saveCLILoginAPIKey(ctx, key); e != nil {
			return e
		}
	}
	m.Secure.Clear()
	m.commandClaudeTokens = nil
	m.Settings.InvalidateSettingsCache()
	if m.remoteManaged != nil {
		return m.remoteManaged.clear(scope)
	}
	return nil
}
func (m *MainCommand) cliLoginOpenAIStore() (*openaiauth.Service, error) {
	return openaiauth.New(openaiauth.Options{Home: m.Native.Home, CWD: m.Native.CWD, Files: nativeOpenAIAuthFiles{m.Native}, Environment: func() map[string]string { return m.Native.Environment }, SecureRead: m.Secure.ReadSourceJSON, SecureReadAsync: m.Secure.ReadSourceJSONAsync, SecureUpdate: func(ctx context.Context, raw json.RawMessage) (openaiauth.Result, error) {
		value := m.Secure.UpdateSourceJSON(ctx, raw)
		result := openaiauth.Result{Success: value.Success}
		if value.Warning != "" {
			result.Warning = &value.Warning
		}
		return result, nil
	}, NowMillis: func() int64 { return int64(m.Native.NowMillis()) }})
}
func (m *MainCommand) saveCLIOpenAITokens(ctx context.Context, scope *cooperative.Scope, tokens protocol.JSONObject) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	store, e := m.cliLoginOpenAIStore()
	if e != nil {
		return e
	}
	result := store.Save(ctx, tokens)
	if !result.Success {
		if result.Warning != nil {
			return errors.New(*result.Warning)
		}
		return errors.New("Failed to persist OpenAI OAuth tokens")
	}
	return nil
}
func (m *MainCommand) installCLIOpenAITokens(ctx context.Context, scope *cooperative.Scope, tokens protocol.JSONObject) (string, error) {
	if !scope.BelongsTo(m.Owner) {
		return "", cooperative.ErrRestricted
	}
	_ = m.Secure.RemoveLegacyManagedAPIKey(ctx)
	if e := m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		next := current.Clone()
		next.Delete("primaryApiKey")
		return &next, nil
	}); e != nil {
		return "", e
	}
	m.clearNativeCommandLegacyAPIKey()
	result, e := m.Secure.Change(ctx, func(value protocol.JSONObject) error { value.Delete("claudeAiOauth"); return nil })
	if e != nil {
		return "", errors.New("Failed to clear stored Claude OAuth tokens")
	}
	if !result.Success {
		if result.Warning != "" {
			return "", errors.New(result.Warning)
		}
		return "", errors.New("Failed to disable existing Claude auth state")
	}
	if e = m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		next := current.Clone()
		next.Delete("oauthAccount")
		return &next, nil
	}); e != nil {
		return "", e
	}
	if tokens.String("refreshToken") == "" {
		return "", errors.New("OpenAI OAuth tokens are incomplete.")
	}

	m.Secure.Clear()
	m.commandClaudeTokens = nil
	m.Settings.InvalidateSettingsCache()
	// CLI auth handlers run before Source switches STATE.isInteractive on.
	identity, _, _, _, e := m.cliAuthIdentity(ctx, scope, true)
	if e != nil {
		return "", e
	}
	if identity.UsingThirdParty {
		return "OpenAI login is saved, but this shell is configured for a third-party provider.", nil
	}
	source := identity.AuthTokenSource
	if source == "none" {
		source = identity.APIKeySource
	}
	if source != "none" && source != "" {
		return "OpenAI login is saved, but this shell is still using " + source + " for Anthropic-compatible auth.", nil
	}
	return "", nil
}
func (m *MainCommand) saveCLILoginAPIKey(ctx context.Context, key string) error {
	for _, char := range key {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_') {
			return errors.New("Invalid API key format. API key must contain only alphanumeric characters, dashes, and underscores.")
		}
	}
	if key == "" {
		return errors.New("Invalid API key format. API key must contain only alphanumeric characters, dashes, and underscores.")
	}
	saved, e := m.Secure.SaveLegacyManagedAPIKey(ctx, key)
	if e != nil {
		return e
	}
	units := utf16.Encode([]rune(key))
	if len(units) > 20 {
		units = units[len(units)-20:]
	}
	normalized := protocol.QuoteJavaScriptUTF16(units)
	e = m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		next := current.Clone()
		if !saved {
			next.Set("primaryApiKey", quoteMain(key))
		}
		responses, ok := next.Object("customApiKeyResponses")
		if !ok {
			responses = protocol.NewJSONObject()
		}
		var approved []json.RawMessage
		_ = json.Unmarshal(responses.Get("approved"), &approved)
		found := false
		for _, value := range approved {
			found = found || string(value) == string(normalized)
		}
		if !found {
			approved = append(approved, normalized)
		}
		responses.Set("approved", rawMain(approved))
		if responses.Get("rejected") == nil {
			responses.Set("rejected", []byte(`[]`))
		}
		next.Set("customApiKeyResponses", rawMain(responses))
		return &next, nil
	})
	if e == nil {
		m.clearNativeCommandLegacyAPIKey()
	}
	return e
}
func (m *MainCommand) validateCLILoginOrganization(ctx context.Context, scope *cooperative.Scope, client *cliauth.OAuthClient) (string, error) {
	if !scope.BelongsTo(m.Owner) {
		return "", cooperative.ErrRestricted
	}
	if m.Native.Environment["ANTHROPIC_UNIX_SOCKET"] != "" {
		return "", nil
	}
	policy, e := m.Startup.GetSettingsForSource("policySettings")
	if e != nil {
		return "", e
	}
	if policy == nil || policy.String("forceLoginOrgUUID") == "" {
		return "", nil
	}
	identity, tokenValue, _, enabled, e := m.cliAuthIdentity(ctx, scope, true)
	if e != nil {
		return "", e
	}
	if !enabled || tokenValue == nil {
		return "", nil
	}
	tokenValue, e = m.freshCLILoginOrganizationToken(ctx, scope, client, 0)
	if e != nil || tokenValue == nil {
		return "", e
	}
	var tokens protocol.JSONObject
	if e = tokens.UnmarshalJSON(tokenValue.JSON()); e != nil {
		return "", e
	}
	required := policy.String("forceLoginOrgUUID")
	profile, e := cliAuthPhysical(m, scope, func() (*protocol.JSONObject, error) { return client.Profile(ctx, tokens.String("accessToken")), nil })
	if e != nil {
		return "", e
	}
	if profile == nil {
		return "Unable to verify organization for the current authentication token.\nThis machine requires organization " + required + " but the profile could not be fetched.\nThis may be a network error, or the token may lack the user:profile scope required for\nverification (tokens from 'claude setup-token' do not include this scope).\nTry again, or obtain a full-scope token via 'claude auth login'.", nil
	}
	org, _ := profile.Object("organization")
	if org.String("uuid") == required {
		return "", nil
	}
	if identity.AuthTokenSource == "CLAUDE_CODE_OAUTH_TOKEN" || identity.AuthTokenSource == "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR" {
		return fmt.Sprintf("The %s environment variable provides a token for a\ndifferent organization than required by this machine's managed settings.\n\nRequired organization: %s\nToken organization:   %s\n\nRemove the environment variable or obtain a token for the correct organization.", identity.AuthTokenSource, required, org.String("uuid")), nil
	}
	return fmt.Sprintf("Your authentication token belongs to organization %s,\nbut this machine requires organization %s.\n\nPlease log in with the correct organization: claude auth login", org.String("uuid"), required), nil
}

func cliAuthPhysical[T any](m *MainCommand, scope *cooperative.Scope, work func() (T, error)) (T, error) {
	var zero T
	var refs ownerasync.NativeReferences
	binding, e := refs.Bind(m.Owner)
	if e != nil {
		return zero, e
	}
	future, e := nativeOwnedWork(m.Owner, binding, scope, work)
	if e != nil {
		return zero, e
	}
	return future.Await(scope)
}

func currentTokenString(tokens *openaiauth.Value, key string) string {
	var value string
	_ = json.Unmarshal(tokens.Get(key), &value)
	return value
}
