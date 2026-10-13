package cliauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
)

type OAuthClient struct {
	Config              desktopoauth.Config
	HTTP                *http.Client
	NowMillis           func() float64
	Environment         func() map[string]string
	Settings            func(context.Context) (protocol.JSONObject, error)
	UpdateClaudeProfile func(context.Context, protocol.JSONObject) error
	PreviousClaude      func(context.Context) (protocol.JSONObject, protocol.JSONObject, error)
}

func (c *OAuthClient) request(ctx context.Context, method, address string, body json.RawMessage, headers [][2]string, timeout time.Duration, expectedStatusLabel ...string) (protocol.JSONObject, error) {
	client := c.HTTP
	if client == nil {
		return protocol.JSONObject{}, fmt.Errorf("CLI OAuth requires actual transport")
	}
	parentContext := ctx
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var input io.Reader
	if body != nil {
		input = bytes.NewReader(body)
	}
	request, e := http.NewRequestWithContext(ctx, method, address, input)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("User-Agent", "axios/1.15.2")
	request.Header.Set("Accept-Encoding", "gzip, compress, deflate, br")
	for _, h := range headers {
		request.Header.Set(h[0], h[1])
	}
	response, e := client.Do(request)
	if e != nil {
		if parentContext.Err() == nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return protocol.JSONObject{}, fmt.Errorf("timeout of %dms exceeded", timeout.Milliseconds())
		}
		return protocol.JSONObject{}, cliOAuthTransportError(e)
	}
	defer response.Body.Close()
	raw, e := webfetch.ReadAxiosResponseBody(response)
	if e != nil {
		if parentContext.Err() == nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return protocol.JSONObject{}, fmt.Errorf("timeout of %dms exceeded", timeout.Milliseconds())
		}
		return protocol.JSONObject{}, e
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return protocol.JSONObject{}, fmt.Errorf("Request failed with status code %d", response.StatusCode)
	}
	if len(expectedStatusLabel) > 0 && response.StatusCode != 200 {
		_, reason, _ := strings.Cut(response.Status, " ")
		if expectedStatusLabel[0] == "setup-token exchange" {
			return protocol.JSONObject{}, fmt.Errorf("Token exchange failed (%d): %s", response.StatusCode, reason)
		}
		return protocol.JSONObject{}, fmt.Errorf("%s: %s", expectedStatusLabel[0], reason)
	}
	// Axios strips a UTF-8 BOM and leaves an invalid JSON entity as text.
	// Ordinary token-field lookup on that text yields undefined, rather than
	// manufacturing a JSON parser error (including partial Z_SYNC_FLUSH data).
	raw = bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})
	normalized := protocol.DecodeJavaScriptUTF8(raw)
	if !json.Valid(normalized) {
		return protocol.NewJSONObject(), nil
	}
	var value protocol.JSONObject
	if e = value.UnmarshalJSON(normalized); e != nil {
		return protocol.JSONObject{}, e
	}
	return value, nil
}
func authHeaders(token string) [][2]string { return [][2]string{{"Authorization", "Bearer " + token}} }
func (c *OAuthClient) Profile(ctx context.Context, token string) *protocol.JSONObject {
	headers := append(authHeaders(token), [2]string{"Content-Type", "application/json"}, [2]string{"User-Agent", "claude-code/" + provider.SourceCompatibilityVersion}, [2]string{"Cache-Control", "no-cache"})
	profile, e := c.request(ctx, "GET", c.Config.BaseAPIURL+"/api/oauth/profile", nil, headers, 10*time.Second)
	if e != nil {
		return nil
	}
	return &profile
}
func (c *OAuthClient) ExchangeClaude(ctx context.Context, code, state, verifier string, port int, manual bool, expiresIn ...float64) (protocol.JSONObject, error) {
	redirect := fmt.Sprintf("http://localhost:%d/callback", port)
	if manual {
		redirect = c.Config.ManualRedirectURL
	}
	body := protocol.NewJSONObject(protocol.JSONField{Name: "grant_type", Value: quoteLogin("authorization_code")}, protocol.JSONField{Name: "code", Value: quoteLogin(code)}, protocol.JSONField{Name: "redirect_uri", Value: quoteLogin(redirect)}, protocol.JSONField{Name: "client_id", Value: quoteLogin(c.Config.ClientID)}, protocol.JSONField{Name: "code_verifier", Value: quoteLogin(verifier)}, protocol.JSONField{Name: "state", Value: quoteLogin(state)})
	label := "Token exchange failed"
	if len(expiresIn) > 0 {
		body.Set("expires_in", encodeLogin(expiresIn[0]))
		label = "setup-token exchange"
	}
	response, e := c.request(ctx, "POST", c.Config.TokenURL, encodeLogin(body), [][2]string{{"Content-Type", "application/json"}}, 15*time.Second, label)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	return c.formatClaude(response, "", c.Profile(ctx, OAuthFieldString(response.Get("access_token")))), nil
}
func (c *OAuthClient) RefreshClaude(ctx context.Context, token string, scopes []string) (protocol.JSONObject, error) {
	if len(scopes) == 0 {
		scopes = desktopoauth.ClaudeScopes
	}
	body := protocol.NewJSONObject(protocol.JSONField{Name: "grant_type", Value: quoteLogin("refresh_token")}, protocol.JSONField{Name: "refresh_token", Value: quoteLogin(token)}, protocol.JSONField{Name: "client_id", Value: quoteLogin(c.Config.ClientID)}, protocol.JSONField{Name: "scope", Value: quoteLogin(strings.Join(scopes, " "))})
	response, e := c.request(ctx, "POST", c.Config.TokenURL, encodeLogin(body), [][2]string{{"Content-Type", "application/json"}}, 15*time.Second, "Token refresh failed")
	if e != nil {
		return protocol.JSONObject{}, e
	}
	oldGlobal, oldTokens := protocol.NewJSONObject(), protocol.NewJSONObject()
	if c.PreviousClaude != nil {
		var e error
		oldGlobal, oldTokens, e = c.PreviousClaude(ctx)
		if e != nil {
			return protocol.JSONObject{}, e
		}
	}
	account, _ := oldGlobal.Object("oauthAccount")
	have := account.Get("billingType") != nil && account.Get("accountCreatedAt") != nil && account.Get("subscriptionCreatedAt") != nil && oldTokens.Get("subscriptionType") != nil && string(oldTokens.Get("subscriptionType")) != "null" && oldTokens.Get("rateLimitTier") != nil && string(oldTokens.Get("rateLimitTier")) != "null"
	var profile *protocol.JSONObject
	if !have {
		profile = c.Profile(ctx, OAuthFieldString(response.Get("access_token")))
	}
	if profile != nil && c.UpdateClaudeProfile != nil && account.IsObject() && len(account.OwnProperties()) > 0 {
		if e := c.UpdateClaudeProfile(ctx, desktopoauth.ProfileInfo(*profile)); e != nil {
			return protocol.JSONObject{}, e
		}
	}
	value := c.formatClaude(response, token, profile)
	for _, key := range []string{"subscriptionType", "rateLimitTier"} {
		if raw := value.Get(key); raw == nil || string(raw) == "null" {
			if old := oldTokens.Get(key); old != nil {
				value.Set(key, old)
			}
		}
	}
	return value, nil
}
func (c *OAuthClient) formatClaude(response protocol.JSONObject, oldRefresh string, profile *protocol.JSONObject) protocol.JSONObject {
	refresh := response.Get("refresh_token")
	if refresh == nil && oldRefresh != "" {
		refresh = quoteLogin(oldRefresh)
	}
	info := desktopoauth.ProfileInfo(protocol.JSONObject{})
	if profile != nil {
		info = desktopoauth.ProfileInfo(*profile)
	}
	value := protocol.NewJSONObject(protocol.JSONField{Name: "accessToken", Value: response.Get("access_token")}, protocol.JSONField{Name: "refreshToken", Value: refresh}, protocol.JSONField{Name: "expiresAt", Value: oauthLifetime(c.NowMillis(), response.Get("expires_in"))}, protocol.JSONField{Name: "scopes", Value: encodeLogin(desktopoauth.ParseScopes(response.String("scope")))}, protocol.JSONField{Name: "subscriptionType", Value: info.Get("subscriptionType")}, protocol.JSONField{Name: "rateLimitTier", Value: info.Get("rateLimitTier")})
	if profile != nil {
		value.Set("profile", encodeLogin(*profile))
	}
	if account, ok := response.Object("account"); ok {
		organization, _ := response.Object("organization")
		value.Set("tokenAccount", encodeLogin(protocol.NewJSONObject(protocol.JSONField{Name: "uuid", Value: account.Get("uuid")}, protocol.JSONField{Name: "emailAddress", Value: account.Get("email_address")}, protocol.JSONField{Name: "organizationUuid", Value: organization.Get("uuid")})))
	}
	return value
}
func quoteLogin(s string) json.RawMessage {
	return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(s)))
}
func encodeLogin(v any) json.RawMessage {
	if object, ok := v.(protocol.JSONObject); ok {
		raw, _ := object.MarshalJSON()
		return raw
	}
	raw, _ := json.Marshal(v)
	return raw
}

func (c *OAuthClient) ExchangeOpenAI(ctx context.Context, code, redirect, verifier string) (protocol.JSONObject, error) {
	response, e := desktopoauth.ExecuteCLIOpenAITokenRequest(ctx, desktopoauth.CLIClientTransport{HTTP: c.HTTP, Environment: c.Environment, Settings: c.Settings}, [][2]string{{"grant_type", "authorization_code"}, {"code", code}, {"redirect_uri", redirect}, {"client_id", desktopoauth.OpenAICodexClientID}, {"code_verifier", verifier}})
	if e != nil {
		return protocol.JSONObject{}, e
	}
	return desktopoauth.NormalizeOpenAITokens(response, int64(c.NowMillis()))
}

func (c *OAuthClient) FetchRoles(ctx context.Context, token string) (protocol.JSONObject, error) {
	return c.request(ctx, "GET", c.Config.BaseAPIURL+"/api/oauth/claude_cli/roles", nil, append(authHeaders(token), [2]string{"User-Agent", "claude-code/" + provider.SourceCompatibilityVersion}), 30*time.Second)
}
func (c *OAuthClient) CreateAPIKey(ctx context.Context, token string) (string, error) {
	result, e := c.request(ctx, "POST", c.Config.BaseAPIURL+"/api/oauth/claude_cli/create_api_key", nil, append(authHeaders(token), [2]string{"Content-Type", "application/x-www-form-urlencoded"}, [2]string{"User-Agent", "claude-code/" + provider.SourceCompatibilityVersion}), 30*time.Second)
	return result.String("raw_key"), e
}
func (c *OAuthClient) FirstTokenDate(ctx context.Context, token, version string) (json.RawMessage, error) {
	return c.FirstTokenDateHeaders(ctx, append(authHeaders(token), [2]string{"anthropic-beta", "oauth-2025-04-20"}), version)
}
func (c *OAuthClient) FirstTokenDateHeaders(ctx context.Context, auth [][2]string, version string) (json.RawMessage, error) {
	headers := append(append([][2]string{}, auth...), [2]string{"User-Agent", "claude-code/" + provider.SourceCompatibilityVersion})
	result, e := c.request(ctx, "GET", c.Config.BaseAPIURL+"/api/organization/claude_code_first_token_date", nil, headers, 10*time.Second)
	if e != nil {
		return nil, e
	}
	value := result.Get("first_token_date")
	if value == nil {
		value = []byte(`null`)
	}
	return value, nil
}
