package desktopoauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
)

type scopedProxy struct{ value *string }
type proxyContextKey struct{}

func (s *Service) proxyURL(ctx context.Context) (*string, error) {
	if scoped, ok := ctx.Value(proxyContextKey{}).(scopedProxy); ok {
		return scoped.value, nil
	}
	if s.options.ProxyURL != nil {
		return s.options.ProxyURL(ctx)
	}
	settings := record()
	if s.options.Settings != nil {
		var err error
		settings, err = s.options.Settings(ctx)
		if err != nil {
			return nil, err
		}
	}
	proxy := object(object(settings.Get("network")).Get("proxy"))
	mode := proxy.String("mode")
	env := s.options.Environment()
	value := ""
	if mode == "manual" {
		value = strings.TrimSpace(proxy.String("url"))
	} else if mode != "direct" {
		value = strings.TrimSpace(env["CC_HAHA_SYSTEM_PROXY_URL"])
		if value == "" {
			if err := strings.TrimSpace(env["CC_HAHA_SYSTEM_PROXY_ERROR"]); err != "" {
				return nil, errors.New(err)
			}
			for _, name := range []string{"https_proxy", "HTTPS_PROXY", "http_proxy", "HTTP_PROXY"} {
				if env[name] != "" {
					value = env[name]
					break
				}
			}
		}
	}
	if value == "" {
		return nil, nil
	}
	return &value, nil
}
func (s *Service) requestJSON(ctx context.Context, method, address string, body protocol.JSONObject, token string, timeout time.Duration, axios bool) (protocol.JSONObject, error) {
	value, _, err := s.request(ctx, method, address, body, token, timeout, axios)
	return value, err
}
func (s *Service) request(ctx context.Context, method, address string, body protocol.JSONObject, token string, timeout time.Duration, axios bool) (protocol.JSONObject, int, error) {
	proxy, err := s.proxyURL(ctx)
	if err != nil {
		return record(), 0, err
	}
	if !axios && exchangeBypassesProxy(address, s.options.Environment()) {
		proxy = nil
	}
	client := *s.options.HTTPClient
	client.Timeout = 0
	if client.Transport == nil {
		base := http.DefaultTransport.(*http.Transport).Clone()
		if e := routeProxy(base, proxy); e != nil {
			return record(), 0, e
		}
		defer base.CloseIdleConnections()
		client.Transport = base
	} else if base, ok := client.Transport.(*http.Transport); ok {
		transport := base.Clone()
		if e := routeProxy(transport, proxy); e != nil {
			return record(), 0, e
		}
		defer transport.CloseIdleConnections()
		client.Transport = transport
	}
	requestContext, cancel := context.WithCancel(ctx)
	defer cancel()
	timer := time.AfterFunc(timeout, cancel)
	defer timer.Stop()
	var payload io.Reader
	if body.IsObject() {
		payload = bytes.NewReader(encode(body))
	}
	request, err := http.NewRequestWithContext(requestContext, method, address, payload)
	if err != nil {
		return record(), 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	if axios {
		request.Header.Set("Accept", "application/json, text/plain, */*")
		request.Header.Set("User-Agent", "axios/1.15.2")
		request.Header.Set("Accept-Encoding", "gzip, compress, deflate, br")
	}
	if method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/api/oauth/profile") {
		request.Header.Set("User-Agent", "claude-code/"+provider.SourceCompatibilityVersion)
		request.Header.Set("Cache-Control", "no-cache")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if !axios {
		timer.Stop()
	}
	if err != nil {
		return record(), 0, err
	}
	defer response.Body.Close()
	var data []byte
	if axios {
		data, err = webfetch.ReadAxiosResponseBody(response)
	} else {
		data, err = io.ReadAll(response.Body)
	}
	if err != nil {
		return record(), response.StatusCode, err
	}
	status := response.StatusCode
	if status < 200 || status >= 300 {
		if axios {
			return record(), status, fmt.Errorf("Request failed with status code %d", status)
		}
		return record(), status, fmt.Errorf("Token exchange failed (%d): %s", status, protocol.DecodeJavaScriptUTF8(data))
	}
	if !json.Valid(protocol.DecodeJavaScriptUTF8(data)) {
		if axios {
			return record(), status, nil
		}
		return record(), status, errors.New("Invalid token exchange JSON response")
	}
	return object(protocol.DecodeJavaScriptUTF8(data)), status, nil
}

func routeProxy(transport *http.Transport, value *string) error {
	transport.Proxy = nil
	if value == nil {
		return nil
	}
	proxy, err := url.Parse(*value)
	if err != nil {
		return err
	}
	transport.Proxy = http.ProxyURL(proxy)
	return nil
}
func ProfileInfo(profile protocol.JSONObject) protocol.JSONObject {
	organization, account := object(profile.Get("organization")), object(profile.Get("account"))
	subscription := ""
	switch organization.String("organization_type") {
	case "claude_max":
		subscription = "max"
	case "claude_pro":
		subscription = "pro"
	case "claude_enterprise":
		subscription = "enterprise"
	case "claude_team":
		subscription = "team"
	}
	var sub any
	if subscription != "" {
		sub = subscription
	}
	result := record(field("subscriptionType", sub), protocol.JSONField{Name: "rateLimitTier", Value: coalesce(organization.Get("rate_limit_tier"))}, protocol.JSONField{Name: "hasExtraUsageEnabled", Value: coalesce(organization.Get("has_extra_usage_enabled"))}, protocol.JSONField{Name: "billingType", Value: coalesce(organization.Get("billing_type"))})
	for key, name := range map[string]string{"displayName": "display_name", "accountCreatedAt": "created_at"} {
		if truthy(account.Get(name)) {
			result.Set(key, account.Get(name))
		}
	}
	if truthy(organization.Get("subscription_created_at")) {
		result.Set("subscriptionCreatedAt", organization.Get("subscription_created_at"))
	}
	if profile.IsObject() {
		result.Set("rawProfile", encode(profile))
	}
	return result
}
func (s *Service) fetchProfile(ctx context.Context, token string) (protocol.JSONObject, error) {
	c, err := s.options.Config()
	if err != nil {
		return record(), err
	}
	profile, _, err := s.request(ctx, "GET", c.BaseAPIURL+"/api/oauth/profile", protocol.JSONObject{}, token, 10*time.Second, true)
	if err != nil {
		profile = protocol.JSONObject{}
	}
	return ProfileInfo(profile), nil
}
func (s *Service) loadCLITokens(ctx context.Context) (*protocol.JSONObject, error) {
	s.cliMu.Lock()
	defer s.cliMu.Unlock()
	if s.cliCached {
		return s.cliTokens, nil
	}
	value, err := s.readCLITokens(ctx)
	if err == nil {
		s.cliTokens, s.cliCached = value, true
	}
	return value, err
}

// ClearCLICredentialCache is the original auth.cache.clear / keychain.clear
// boundary. Desktop-managed login/logout must not clear the separate CLI slot.
func (s *Service) ClearCLICredentialCache() {
	s.cliMu.Lock()
	s.cliCached, s.cliTokens = false, nil
	s.options.Secure.Clear()
	s.cliMu.Unlock()
}

func (s *Service) readCLITokens(ctx context.Context) (*protocol.JSONObject, error) {
	env := s.options.Environment()
	if envTruthy(env["CLAUDE_CODE_SIMPLE"]) {
		return nil, nil
	}
	if token := env["CLAUDE_CODE_OAUTH_TOKEN"]; token != "" {
		value := record(field("accessToken", token), field("refreshToken", nil), field("expiresAt", nil), field("scopes", []string{"user:inference"}), field("subscriptionType", nil), field("rateLimitTier", nil))
		return &value, nil
	}
	if s.options.ReadTokenFD != nil {
		token, err := s.options.ReadTokenFD(ctx)
		if err != nil {
			return nil, err
		}
		if token != "" {
			value := record(field("accessToken", token), field("refreshToken", nil), field("expiresAt", nil), field("scopes", []string{"user:inference"}), field("subscriptionType", nil), field("rateLimitTier", nil))
			return &value, nil
		}
	}
	stored, err := s.options.Secure.Read(ctx)
	if err != nil || stored == nil {
		// getClaudeAIOAuthTokens catches secure-storage errors and returns null.
		return nil, nil
	}
	tokens := object(stored.Get("claudeAiOauth"))
	if !truthy(tokens.Get("accessToken")) {
		return nil, nil
	}
	return &tokens, nil
}
func exchangeBypassesProxy(address string, env map[string]string) bool {
	u, err := url.Parse(address)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	value := env["no_proxy"]
	if value == "" {
		value = env["NO_PROXY"]
	}
	for _, item := range strings.FieldsFunc(value, func(c rune) bool { return c == ',' || c == ' ' || c == '\t' || c == '\n' || c == '\r' }) {
		pattern := strings.ToLower(strings.TrimSpace(item))
		if pattern == "*" {
			return true
		}
		if strings.Contains(pattern, ":") {
			port := u.Port()
			if port == "" {
				port = "80"
				if u.Scheme == "https" {
					port = "443"
				}
			}
			if strings.HasPrefix(pattern, "[") {
				if at := strings.LastIndex(pattern, "]:"); at >= 0 && host == pattern[1:at] && port == pattern[at+2:] {
					return true
				}
			}
			if strings.Count(pattern, ":") == 1 {
				if host+":"+port == pattern {
					return true
				}
				continue
			}
			if host == strings.Trim(pattern, "[]") {
				return true
			}
			continue
		}
		if strings.HasPrefix(pattern, ".") {
			if host == pattern[1:] || strings.HasSuffix(host, pattern) {
				return true
			}
		} else if host == pattern {
			return true
		}
	}
	return false
}
func (s *Service) refresh(ctx context.Context, refreshToken string, requested json.RawMessage) (protocol.JSONObject, error) {
	proxy, err := s.proxyURL(ctx)
	if err != nil {
		return record(), err
	}
	ctx = context.WithValue(ctx, proxyContextKey{}, scopedProxy{proxy})
	c, err := s.options.Config()
	if err != nil {
		return record(), err
	}
	scopes := []string{}
	_ = json.Unmarshal(requested, &scopes)
	if len(scopes) == 0 {
		scopes = append([]string(nil), ClaudeScopes...)
	}
	body := record(field("grant_type", "refresh_token"), field("refresh_token", refreshToken), field("client_id", c.ClientID), field("scope", strings.Join(scopes, " ")))
	data, status, err := s.request(ctx, "POST", c.TokenURL, body, "", 15*time.Second, true)
	if err != nil {
		return record(), err
	}
	if status != 200 {
		return record(), fmt.Errorf("Token refresh failed: %s", http.StatusText(status))
	}
	global, err := s.options.Global.Get()
	if err != nil {
		return record(), err
	}
	config := global.Clone()
	existing, err := s.options.LoadCLITokens(ctx)
	if err != nil {
		return record(), err
	}
	previous := record()
	if existing != nil {
		previous = *existing
	}
	account := object(config.Get("oauthAccount"))
	have := account.Get("billingType") != nil && account.Get("accountCreatedAt") != nil && account.Get("subscriptionCreatedAt") != nil && coalesce(previous.Get("subscriptionType")) != nil && string(coalesce(previous.Get("subscriptionType"))) != "null" && string(coalesce(previous.Get("rateLimitTier"))) != "null"
	profile := record()
	if !have {
		profile, err = s.options.FetchProfile(ctx, data.String("access_token"))
		if err != nil {
			return record(), err
		}
	}
	if !have && config.Get("oauthAccount") != nil && string(config.Get("oauthAccount")) != "null" {
		updates := record()
		for _, name := range []string{"displayName", "accountCreatedAt", "subscriptionCreatedAt"} {
			if value := profile.Get(name); value != nil {
				updates.Set(name, value)
			}
		}
		if v := profile.Get("hasExtraUsageEnabled"); string(v) == "true" || string(v) == "false" {
			updates.Set("hasExtraUsageEnabled", v)
		}
		if v := profile.Get("billingType"); v != nil && string(v) != "null" {
			updates.Set("billingType", v)
		}
		if len(updates.OwnProperties()) > 0 {
			if err = s.options.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
				next := current.Clone()
				if truthy(current.Get("oauthAccount")) {
					old := object(current.Get("oauthAccount"))
					old.Assign(updates)
					next.Set("oauthAccount", encode(old))
				}
				return &next, nil
			}); err != nil {
				return record(), err
			}
		}
	}
	expires := float64(s.options.Now().UnixMilli()) + number(data.Get("expires_in"))*1000
	result := record(protocol.JSONField{Name: "accessToken", Value: data.Get("access_token")}, protocol.JSONField{Name: "refreshToken", Value: coalesce(data.Get("refresh_token"), encode(refreshToken))}, protocol.JSONField{Name: "expiresAt", Value: numberJSON(expires)}, field("scopes", ParseScopes(data.String("scope"))), protocol.JSONField{Name: "subscriptionType", Value: coalesce(profile.Get("subscriptionType"), previous.Get("subscriptionType"))}, protocol.JSONField{Name: "rateLimitTier", Value: coalesce(profile.Get("rateLimitTier"), previous.Get("rateLimitTier"))})
	return result, nil
}
