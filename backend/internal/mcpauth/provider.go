package mcpauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type tokenReply struct {
	tokens *protocol.JSONObject
	err    error
	done   chan struct{}
}
type Provider struct {
	service                                                  *Service
	name, key                                                string
	config                                                   protocol.JSONObject
	redirect                                                 string
	handleRedirect, skipBrowser                              bool
	onURL                                                    func(string) error
	mu                                                       sync.Mutex
	state, verifier, authorizationURL, scopes, pendingStepUp string
	metadata                                                 *protocol.JSONObject
	refresh                                                  *tokenReply
}

func (p *Provider) ClientMetadata() protocol.JSONObject {
	value := record(field("client_name", "Claude Code ("+p.name+")"), field("redirect_uris", []string{p.redirect}), field("grant_types", []string{"authorization_code", "refresh_token"}), field("response_types", []string{"code"}), field("token_endpoint_auth_method", "none"))
	p.mu.Lock()
	metadata := p.metadata
	p.mu.Unlock()
	if scope := scopeFromMetadata(metadata); scope != "" {
		value.Set("scope", quote(scope))
	}
	return value
}
func (p *Provider) ClientMetadataURL() string {
	if value := p.service.options.Environment["MCP_OAUTH_CLIENT_METADATA_URL"]; value != "" {
		return value
	}
	return ClientMetadataURL
}
func scopeFromMetadata(value *protocol.JSONObject) string {
	if value == nil {
		return ""
	}
	for _, key := range []string{"scope", "default_scope"} {
		if isString(value.Get(key)) {
			return value.String(key)
		}
	}
	var scopes []string
	if json.Unmarshal(value.Get("scopes_supported"), &scopes) == nil && scopes != nil {
		return strings.Join(scopes, " ")
	}
	return ""
}
func (p *Provider) SetMetadata(value protocol.JSONObject) {
	p.mu.Lock()
	defer p.mu.Unlock()
	copy := value.Clone()
	p.metadata = &copy
}
func (p *Provider) MarkStepUpPending(scope string) {
	p.mu.Lock()
	p.pendingStepUp = scope
	p.mu.Unlock()
}
func (p *Provider) State() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state == "" {
		bytes := make([]byte, 32)
		if _, err := io.ReadFull(p.service.options.Random, bytes); err != nil {
			return "", err
		}
		p.state = base64.RawURLEncoding.EncodeToString(bytes)
	}
	return p.state, nil
}
func (p *Provider) ClientInformation(ctx context.Context) (*protocol.JSONObject, error) {
	data, err := p.service.read(ctx)
	if err != nil {
		return nil, err
	}
	entry := object(object(data.Get("mcpOAuth")).Get(p.key))
	if truthy(entry.Get("clientId")) {
		value := protocol.NewJSONObject(protocol.JSONField{Name: "client_id", Value: entry.Get("clientId")}, protocol.JSONField{Name: "client_secret", Value: entry.Get("clientSecret")})
		return &value, nil
	}
	client := object(p.config.Get("oauth")).Get("clientId")
	if truthy(client) {
		configured := object(object(data.Get("mcpOAuthClientConfig")).Get(p.key))
		value := protocol.NewJSONObject(protocol.JSONField{Name: "client_id", Value: client}, protocol.JSONField{Name: "client_secret", Value: configured.Get("clientSecret")})
		return &value, nil
	}
	return nil, nil
}
func (p *Provider) updateEntry(ctx context.Context, operation func(protocol.JSONObject) error) error {
	return p.service.change(ctx, func(data protocol.JSONObject) error {
		slots := object(data.Get("mcpOAuth"))
		entry := object(slots.Get(p.key))
		if err := operation(entry); err != nil {
			return err
		}
		slots.Set(p.key, raw(entry))
		data.Set("mcpOAuth", raw(slots))
		return nil
	})
}
func (p *Provider) SaveClientInformation(ctx context.Context, value protocol.JSONObject) error {
	return p.updateEntry(ctx, func(entry protocol.JSONObject) error {
		entry.Set("serverName", quote(p.name))
		entry.Set("serverUrl", p.config.Get("url"))
		entry.Set("clientId", value.Get("client_id"))
		entry.Set("clientSecret", value.Get("client_secret"))
		if !truthy(entry.Get("accessToken")) {
			entry.Set("accessToken", quote(""))
		}
		if !truthy(entry.Get("expiresAt")) {
			entry.Set("expiresAt", []byte("0"))
		}
		return nil
	})
}
func (p *Provider) SaveTokens(ctx context.Context, tokens protocol.JSONObject) error {
	p.mu.Lock()
	p.pendingStepUp = ""
	p.mu.Unlock()
	return p.updateEntry(ctx, func(entry protocol.JSONObject) error {
		entry.Set("serverName", quote(p.name))
		entry.Set("serverUrl", p.config.Get("url"))
		entry.Set("accessToken", tokens.Get("access_token"))
		entry.Set("refreshToken", tokens.Get("refresh_token"))
		expires := number(tokens.Get("expires_in"))
		if !truthy(tokens.Get("expires_in")) {
			expires = 3600
		}
		entry.Set("expiresAt", jsonNumber(float64(p.service.options.Now().UnixMilli())+expires*1000))
		entry.Set("scope", tokens.Get("scope"))
		return nil
	})
}
func (p *Provider) SaveDiscoveryState(ctx context.Context, state protocol.JSONObject) error {
	return p.updateEntry(ctx, func(entry protocol.JSONObject) error {
		entry.Set("serverName", quote(p.name))
		entry.Set("serverUrl", p.config.Get("url"))
		if !truthy(entry.Get("accessToken")) {
			entry.Set("accessToken", quote(""))
		}
		if !truthy(entry.Get("expiresAt")) {
			entry.Set("expiresAt", []byte("0"))
		}
		urls := protocol.NewJSONObject(protocol.JSONField{Name: "authorizationServerUrl", Value: state.Get("authorizationServerUrl")}, protocol.JSONField{Name: "resourceMetadataUrl", Value: state.Get("resourceMetadataUrl")})
		entry.Set("discoveryState", raw(urls))
		return nil
	})
}
func (p *Provider) DiscoveryState(ctx context.Context) (*protocol.JSONObject, error) {
	entry, err := p.service.entry(ctx, p.key)
	if err != nil {
		return nil, err
	}
	cached := object(entry.Get("discoveryState"))
	if truthy(cached.Get("authorizationServerUrl")) {
		value := protocol.NewJSONObject(protocol.JSONField{Name: "authorizationServerUrl", Value: cached.Get("authorizationServerUrl")}, protocol.JSONField{Name: "resourceMetadataUrl", Value: cached.Get("resourceMetadataUrl")}, protocol.JSONField{Name: "resourceMetadata", Value: cached.Get("resourceMetadata")}, protocol.JSONField{Name: "authorizationServerMetadata", Value: cached.Get("authorizationServerMetadata")})
		return &value, nil
	}
	if hint := object(p.config.Get("oauth")).String("authServerMetadataUrl"); hint != "" {
		metadata, err := p.service.fetchAuthMetadata(ctx, p.name, p.config.String("url"), hint, "")
		if err == nil && metadata != nil {
			value := record(field("authorizationServerUrl", metadata.String("issuer")), field("authorizationServerMetadata", *metadata))
			return &value, nil
		}
	}
	return nil, nil
}
func (p *Provider) InvalidateCredentials(ctx context.Context, scope string) error {
	data, err := p.service.read(ctx)
	if err != nil {
		return err
	}
	slots := object(data.Get("mcpOAuth"))
	if slots.Get(p.key) == nil {
		return nil
	}
	if scope == "verifier" {
		p.mu.Lock()
		p.verifier = ""
		p.mu.Unlock()
		return nil
	}
	return p.service.change(ctx, func(data protocol.JSONObject) error {
		slots := object(data.Get("mcpOAuth"))
		entry := object(slots.Get(p.key))
		switch scope {
		case "all":
			slots.Delete(p.key)
			data.Set("mcpOAuth", raw(slots))
			return nil
		case "client":
			entry.Delete("clientId")
			entry.Delete("clientSecret")
		case "tokens":
			entry.Set("accessToken", quote(""))
			entry.Delete("refreshToken")
			entry.Set("expiresAt", []byte("0"))
		case "discovery":
			entry.Delete("discoveryState")
			entry.Delete("stepUpScope")
		}
		slots.Set(p.key, raw(entry))
		data.Set("mcpOAuth", raw(slots))
		return nil
	})
}
func (p *Provider) SaveCodeVerifier(value string) { p.mu.Lock(); p.verifier = value; p.mu.Unlock() }
func (p *Provider) CodeVerifier() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.verifier == "" {
		return "", errors.New("No code verifier saved")
	}
	return p.verifier, nil
}
func (p *Provider) RedirectToAuthorization(ctx context.Context, address string) error {
	u, err := parseURL(address)
	if err != nil {
		return err
	}
	scope := u.SearchParams().Get("scope")
	if scope == "" {
		p.mu.Lock()
		scope = scopeFromMetadata(p.metadata)
		p.mu.Unlock()
	}
	p.mu.Lock()
	p.authorizationURL = u.Href(false)
	if scope != "" {
		p.scopes = scope
	}
	p.mu.Unlock()
	if scope != "" && !p.handleRedirect {
		entry, err := p.service.entry(ctx, p.key)
		if err != nil {
			return err
		}
		if len(entry.OwnProperties()) > 0 {
			if err = p.updateEntry(ctx, func(entry protocol.JSONObject) error { entry.Set("stepUpScope", quote(scope)); return nil }); err != nil {
				return err
			}
		}
	}
	if !p.handleRedirect {
		return nil
	}
	if u.Scheme() != "http" && u.Scheme() != "https" {
		return errors.New("Invalid authorization URL: must use http:// or https:// scheme")
	}
	if p.onURL != nil {
		if err = p.onURL(u.Href(false)); err != nil {
			return err
		}
	}
	if !p.skipBrowser && p.service.options.OpenBrowser != nil {
		_, err = p.service.options.OpenBrowser(ctx, u.Href(false))
		return err
	}
	return nil
}
func (p *Provider) Tokens(ctx context.Context) (*protocol.JSONObject, error) {
	entry, err := p.service.entry(ctx, p.key)
	if err != nil {
		return nil, err
	}
	expires := (number(entry.Get("expiresAt")) - float64(p.service.options.Now().UnixMilli())) / 1000
	if envTruthy(p.service.options.Environment["CLAUDE_CODE_ENABLE_XAA"]) && truthy(object(p.config.Get("oauth")).Get("xaa")) && !truthy(entry.Get("refreshToken")) && (!truthy(entry.Get("accessToken")) || expires <= 300) {
		refreshed, err := p.runRefresh(ctx, func() (*protocol.JSONObject, error) { return p.xaaRefresh(ctx) })
		if err == nil && refreshed != nil {
			return refreshed, nil
		}
	}
	if len(entry.OwnProperties()) == 0 {
		return nil, nil
	}
	p.mu.Lock()
	stepUp := p.pendingStepUp
	p.mu.Unlock()
	needsStepUp := false
	if stepUp != "" {
		current := strings.Split(entry.String("scope"), " ")
		for _, scope := range strings.Split(stepUp, " ") {
			found := false
			for _, saved := range current {
				if saved == scope {
					found = true
					break
				}
			}
			if !found {
				needsStepUp = true
				break
			}
		}
	}
	if expires <= 0 && !truthy(entry.Get("refreshToken")) {
		return nil, nil
	}
	if expires <= 300 && truthy(entry.Get("refreshToken")) && !needsStepUp {
		refreshed, err := p.runRefresh(ctx, func() (*protocol.JSONObject, error) { return p.RefreshAuthorization(ctx, entry.String("refreshToken")) })
		if err == nil && refreshed != nil {
			return refreshed, nil
		}
	}
	value := protocol.NewJSONObject(protocol.JSONField{Name: "access_token", Value: entry.Get("accessToken")}, protocol.JSONField{Name: "refresh_token", Value: entry.Get("refreshToken")}, protocol.JSONField{Name: "expires_in", Value: jsonNumber(expires)}, protocol.JSONField{Name: "scope", Value: entry.Get("scope")}, field("token_type", "Bearer"))
	if needsStepUp {
		value.Delete("refresh_token")
	}
	return &value, nil
}
func (p *Provider) runRefresh(ctx context.Context, work func() (*protocol.JSONObject, error)) (*protocol.JSONObject, error) {
	p.mu.Lock()
	ongoing := p.refresh
	if ongoing == nil {
		ongoing = &tokenReply{done: make(chan struct{})}
		p.refresh = ongoing
		p.mu.Unlock()
		tokens, err := work()
		p.mu.Lock()
		ongoing.tokens, ongoing.err = tokens, err
		p.refresh = nil
		close(ongoing.done)
		p.mu.Unlock()
	} else {
		p.mu.Unlock()
		select {
		case <-ongoing.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return ongoing.tokens, ongoing.err
}
func (p *Provider) Token(ctx context.Context) (string, error) {
	tokens, err := p.Tokens(ctx)
	if err != nil || tokens == nil {
		return "", err
	}
	return tokens.String("access_token"), nil
}
func (p *Provider) Authorize(ctx context.Context, challenge mcpclient.AuthChallenge) error {
	parameters := wwwAuthenticate(challenge.WWWAuthenticate)
	if challenge.Status == 403 && strings.Contains(challenge.WWWAuthenticate, "insufficient_scope") && parameters.scope != "" {
		p.MarkStepUpPending(parameters.scope)
	}
	result, err := p.service.auth(ctx, p, authOptions{scope: parameters.scope, resourceMetadataURL: parameters.resource})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		p.service.debug(p.name, "OAuth challenge requires explicit authentication")
		return needsAuth(p.config, challenge)
	}
	if result != "AUTHORIZED" {
		return needsAuth(p.config, challenge)
	}
	return nil
}
func number(value json.RawMessage) float64 {
	if len(value) == 0 {
		return math.NaN()
	}
	var n any
	if json.Unmarshal(value, &n) != nil {
		return math.NaN()
	}
	switch v := n.(type) {
	case nil:
		return 0
	case float64:
		return v
	case bool:
		if v {
			return 1
		}
		return 0
	case []any:
		encoded, err := protocol.ExactJavaScriptStringJSON(value)
		if err != nil {
			return math.NaN()
		}
		return number(encoded)
	case string:
		text := strings.TrimFunc(v, func(r rune) bool { return unicode.IsSpace(r) || r == 0xfeff })
		if text == "" {
			return 0
		}
		for prefix, base := range map[string]int{"0x": 16, "0b": 2, "0o": 8} {
			if strings.HasPrefix(strings.ToLower(text), prefix) {
				digits := text[2:]
				if strings.ContainsAny(digits, "_+-") {
					return math.NaN()
				}
				integer, ok := new(big.Int).SetString(digits, base)
				if !ok || strings.HasPrefix(text[2:], "+") || strings.HasPrefix(text[2:], "-") {
					return math.NaN()
				}
				result, _ := new(big.Float).SetInt(integer).Float64()
				return result
			}
		}
		if text == "Infinity" || text == "+Infinity" {
			return math.Inf(1)
		}
		if text == "-Infinity" {
			return math.Inf(-1)
		}
		if !regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`).MatchString(text) {
			return math.NaN()
		}
		parsed, err := strconv.ParseFloat(text, 64)
		if err == nil {
			return parsed
		}
		if errors.Is(err, strconv.ErrRange) {
			return parsed
		}
	}
	return math.NaN()
}
func jsonNumber(value float64) json.RawMessage {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return []byte("null")
	}
	return []byte(strconv.FormatFloat(value, 'g', -1, 64))
}
func isString(value json.RawMessage) bool {
	_, err := protocol.DecodeJavaScriptJSONString(value)
	return err == nil
}
func (p *Provider) pkce() (string, string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~"
	verifier := make([]byte, 0, 43)
	for len(verifier) < 43 {
		bytes := make([]byte, 43-len(verifier))
		if _, err := io.ReadFull(p.service.options.Random, bytes); err != nil {
			return "", "", err
		}
		for _, b := range bytes {
			if b < 198 {
				verifier = append(verifier, alphabet[int(b)%len(alphabet)])
			}
		}
	}
	text := string(verifier)
	sum := sha256.Sum256([]byte(text))
	return text, base64.RawURLEncoding.EncodeToString(sum[:]), nil
}
