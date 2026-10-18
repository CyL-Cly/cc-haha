package mcpauth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var keySanitize = regexp.MustCompile(`[^a-zA-Z0-9]`)
var transientMessage = regexp.MustCompile(`(?i)timeout|timed out|etimedout|econnreset`)

// The directory layout, ten-second stale threshold and five-second heartbeat
// retain proper-lockfile's default lease used by the original provider.
func (p *Provider) refreshLock(ctx context.Context) (func(), error) {
	directory := p.service.options.ConfigDir
	if p.service.options.NativeConfigDirectory != nil {
		directory = p.service.options.NativeConfigDirectory()
	}
	// refreshAuthorization captures claudeDir before its first mkdir await.
	// Every retry, heartbeat and release keeps that same admitted lease target.
	if err := p.service.options.MkdirAll(directory, 0777); err != nil {
		return nil, err
	}
	path := filepath.Join(directory, "mcp-refresh-"+keySanitize.ReplaceAllString(p.key, "_")+".lock.lock")
	for retry := 0; retry < 5; retry++ {
		err := os.Mkdir(path, 0777)
		if err == nil {
			var mutex sync.Mutex
			done := make(chan struct{})
			var once sync.Once
			stamp := p.service.options.Now()
			owned, statErr := os.Stat(path)
			if statErr != nil {
				_ = os.Remove(path)
				return nil, statErr
			}
			_ = os.Chtimes(path, stamp, stamp)
			go func() {
				ticker := time.NewTicker(5 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-done:
						return
					case <-ticker.C:
						mutex.Lock()
						stat, err := os.Stat(path)
						if err == nil && os.SameFile(owned, stat) && stat.ModTime().Equal(stamp) {
							stamp = p.service.options.Now()
							_ = os.Chtimes(path, stamp, stamp)
						}
						mutex.Unlock()
					}
				}
			}()
			return func() {
				once.Do(func() {
					close(done)
					mutex.Lock()
					if current, e := os.Stat(path); e == nil && os.SameFile(owned, current) && current.ModTime().Equal(stamp) {
						_ = os.Remove(path)
					}
					mutex.Unlock()
				})
			}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if stat, e := os.Stat(path); e == nil && stat.ModTime().Before(p.service.options.Now().Add(-10*time.Second)) {
			if e = os.Remove(path); e == nil {
				retry--
				continue
			}
		}
		if err = p.service.options.Sleep(ctx, time.Duration(1000+p.service.options.RandomFloat()*1000)*time.Millisecond); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("Lock file is already being held")
}
func (p *Provider) RefreshAuthorization(ctx context.Context, refresh string) (*protocol.JSONObject, error) {
	release, err := p.refreshLock(ctx)
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if release != nil {
		defer release()
	}
	p.service.options.Secure.Clear()
	entry, err := p.service.entry(ctx, p.key)
	if err != nil {
		return nil, err
	}
	if len(entry.OwnProperties()) > 0 {
		expires := (number(entry.Get("expiresAt")) - float64(p.service.options.Now().UnixMilli())) / 1000
		if expires > 300 {
			value := protocol.NewJSONObject(protocol.JSONField{Name: "access_token", Value: entry.Get("accessToken")}, protocol.JSONField{Name: "refresh_token", Value: entry.Get("refreshToken")}, protocol.JSONField{Name: "expires_in", Value: jsonNumber(expires)}, protocol.JSONField{Name: "scope", Value: entry.Get("scope")}, field("token_type", "Bearer"))
			return &value, nil
		}
		if truthy(entry.Get("refreshToken")) {
			refresh = entry.String("refreshToken")
		}
	}
	return p.doRefresh(ctx, refresh)
}
func (p *Provider) refreshEvent(outcome, reason string) {
	fields := flowFields(p.config)
	if reason != "" {
		fields.Set("reason", quote(reason))
	}
	p.service.event("tengu_mcp_oauth_refresh_"+outcome, fields)
}
func (p *Provider) doRefresh(ctx context.Context, refresh string) (*protocol.JSONObject, error) {
	for attempt := 1; attempt <= 3; attempt++ {
		p.mu.Lock()
		metadata := p.metadata
		p.mu.Unlock()
		var err error
		if metadata == nil {
			var cached *protocol.JSONObject
			cached, err = p.DiscoveryState(ctx)
			if err == nil && cached != nil {
				if m := cached.Get("authorizationServerMetadata"); truthy(m) {
					value := object(m)
					metadata = &value
				} else if address := cached.String("authorizationServerUrl"); address != "" {
					metadata, err = p.service.DiscoverAuthorizationServerMetadata(ctx, address)
				}
			}
		}
		if err == nil && metadata == nil {
			metadata, err = p.service.fetchAuthMetadata(ctx, p.name, p.config.String("url"), object(p.config.Get("oauth")).String("authServerMetadataUrl"), "")
		}
		if err == nil && metadata == nil {
			p.refreshEvent("failure", "metadata_discovery_failed")
			return nil, nil
		}
		if err == nil {
			p.SetMetadata(*metadata)
			var client *protocol.JSONObject
			client, err = p.ClientInformation(ctx)
			if err == nil && client == nil {
				p.refreshEvent("failure", "no_client_info")
				return nil, nil
			}
			if err == nil {
				var tokens protocol.JSONObject
				resource := p.config.String("url")
				if u, parseErr := parseURL(resource); parseErr == nil {
					resource = u.Href(false)
				}
				tokens, err = p.service.refreshRequest(ctx, p.config.String("url"), metadata, client, refresh, resource, true)
				if err == nil {
					if err = p.SaveTokens(ctx, tokens); err == nil {
						p.refreshEvent("success", "")
						return &tokens, nil
					}
				}
			}
		}
		var oauth *OAuthError
		if errors.As(err, &oauth) && oauth.Code == "invalid_grant" {
			p.service.options.Secure.Clear()
			entry, readErr := p.service.entry(ctx, p.key)
			if readErr != nil {
				return nil, readErr
			}
			expires := (number(entry.Get("expiresAt")) - float64(p.service.options.Now().UnixMilli())) / 1000
			if expires > 300 {
				value := protocol.NewJSONObject(protocol.JSONField{Name: "access_token", Value: entry.Get("accessToken")}, protocol.JSONField{Name: "refresh_token", Value: entry.Get("refreshToken")}, protocol.JSONField{Name: "expires_in", Value: jsonNumber(expires)}, protocol.JSONField{Name: "scope", Value: entry.Get("scope")}, field("token_type", "Bearer"))
				return &value, nil
			}
			if err = p.InvalidateCredentials(ctx, "tokens"); err != nil {
				return nil, err
			}
			p.refreshEvent("failure", "invalid_grant")
			return nil, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		retryable := transientMessage.MatchString(err.Error()) || errors.As(err, &oauth) && oauth.retryable()
		if !retryable || attempt >= 3 {
			reason := "request_failed"
			if retryable {
				reason = "transient_retries_exhausted"
			}
			p.refreshEvent("failure", reason)
			return nil, nil
		}
		if err = p.service.options.Sleep(ctx, time.Duration(1<<(attempt-1))*time.Second); err != nil {
			return nil, err
		}
	}
	return nil, nil
}
func (p *Provider) Revoke(ctx context.Context, preserve bool) error {
	entry, err := p.service.entry(ctx, p.key)
	if err != nil {
		return err
	}
	if len(entry.OwnProperties()) == 0 {
		return nil
	}
	if truthy(entry.Get("accessToken")) || truthy(entry.Get("refreshToken")) {
		address := p.config.String("url")
		if state := object(entry.Get("discoveryState")); state.String("authorizationServerUrl") != "" {
			address = state.String("authorizationServerUrl")
		}
		metadata, err := p.service.fetchAuthMetadata(ctx, p.name, address, object(p.config.Get("oauth")).String("authServerMetadataUrl"), "")
		if err == nil && metadata != nil {
			if endpoint := metadata.String("revocation_endpoint"); endpoint != "" {
				method := "client_secret_basic"
				supported := metadata.Get("revocation_endpoint_auth_methods_supported")
				if supported == nil {
					supported = metadata.Get("token_endpoint_auth_methods_supported")
				}
				if supported != nil && !containsString(supported, "client_secret_basic") && containsString(supported, "client_secret_post") {
					method = "client_secret_post"
				}
				for _, kind := range []string{"refresh_token", "access_token"} {
					key := "refreshToken"
					if kind == "access_token" {
						key = "accessToken"
					}
					if truthy(entry.Get(key)) {
						_ = p.service.revokeToken(ctx, endpoint, entry.String(key), kind, entry.String("clientId"), entry.String("clientSecret"), entry.String("accessToken"), method)
					}
				}
			}
		}
	}
	if err = p.service.ClearTokens(ctx, p.name, p.config); err != nil {
		return err
	}
	if preserve && (truthy(entry.Get("stepUpScope")) || truthy(entry.Get("discoveryState"))) {
		return p.updateEntry(ctx, func(value protocol.JSONObject) error {
			value.Set("serverName", quote(p.name))
			value.Set("serverUrl", p.config.Get("url"))
			if value.Get("accessToken") == nil || string(value.Get("accessToken")) == "null" {
				value.Set("accessToken", quote(""))
			}
			if value.Get("expiresAt") == nil || string(value.Get("expiresAt")) == "null" {
				value.Set("expiresAt", []byte("0"))
			}
			if truthy(entry.Get("stepUpScope")) {
				value.Set("stepUpScope", entry.Get("stepUpScope"))
			}
			if truthy(entry.Get("discoveryState")) {
				state := object(entry.Get("discoveryState"))
				value.Set("discoveryState", raw(protocol.NewJSONObject(protocol.JSONField{Name: "authorizationServerUrl", Value: state.Get("authorizationServerUrl")}, protocol.JSONField{Name: "resourceMetadataUrl", Value: state.Get("resourceMetadataUrl")})))
			}
			return nil
		})
	}
	return nil
}
func (s *Service) Revoke(ctx context.Context, name string, config protocol.JSONObject, preserve bool) error {
	provider, err := s.Provider(ctx, name, config)
	if err != nil {
		return err
	}
	return provider.Revoke(ctx, preserve)
}
func formDelete(form formPairs, keys ...string) formPairs {
	result := formPairs{}
	for _, pair := range form {
		drop := false
		for _, key := range keys {
			if pair.key == key {
				drop = true
			}
		}
		if !drop {
			result = append(result, pair)
		}
	}
	return result
}
func (s *Service) revokeToken(ctx context.Context, endpoint, token, hint, clientID, secret, access, method string) error {
	params := formPairs{{"token", token}, {"token_type_hint", hint}}
	headers := map[string][]string{"Content-Type": {"application/x-www-form-urlencoded"}}
	if clientID != "" && secret != "" {
		if method == "client_secret_post" {
			params.set("client_id", clientID)
			params.set("client_secret", secret)
		} else {
			headers["Authorization"] = []string{"Basic " + basicRevocation(clientID, secret)}
		}
	} else if clientID != "" {
		params.set("client_id", clientID)
	}
	response, _, err := s.request(ctx, "POST", endpoint, headers, params.encode(), false)
	if err != nil {
		return err
	}
	if response.StatusCode == 401 && access != "" {
		params = formDelete(params, "client_id", "client_secret")
		headers["Authorization"] = []string{"Bearer " + access}
		response, _, err = s.request(ctx, "POST", endpoint, headers, params.encode(), false)
		if err != nil {
			return err
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("OAuth token revocation failed")
	}
	return nil
}
func basicRevocation(client, secret string) string {
	return base64Text(encodeURIComponent(client) + ":" + encodeURIComponent(secret))
}
func encodeURIComponent(value string) string {
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for _, b := range []byte(value) {
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.ContainsRune("-_.!~*'()", rune(b)) {
			out.WriteByte(b)
		} else {
			out.WriteByte('%')
			out.WriteByte(hex[b>>4])
			out.WriteByte(hex[b&15])
		}
	}
	return out.String()
}
