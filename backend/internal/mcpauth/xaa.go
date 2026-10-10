package mcpauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const tokenExchangeGrant = "urn:ietf:params:oauth:grant-type:token-exchange"
const jwtBearerGrant = "urn:ietf:params:oauth:grant-type:jwt-bearer"
const idJAGType = "urn:ietf:params:oauth:token-type:id-jag"
const idTokenType = "urn:ietf:params:oauth:token-type:id_token"

type XaaTokenExchangeError struct {
	Message            string
	ShouldClearIDToken bool
}

func (e *XaaTokenExchangeError) Error() string { return e.Message }

var sensitiveToken = regexp.MustCompile(`"(access_token|refresh_token|id_token|assertion|subject_token|client_secret)"\s*:\s*"[^"]*"`)

func redactTokens(data []byte) string {
	return sensitiveToken.ReplaceAllString(string(data), `"$1":"[REDACTED]"`)
}
func redactParsedTokens(data []byte) string {
	normalized, err := protocol.ParseAndStringifyJavaScriptJSON(data)
	if err != nil {
		return redactTokens(data)
	}
	return redactTokens(normalized)
}
func normalizeURL(value string) string {
	if u, err := parseURL(value); err == nil {
		return strings.TrimSuffix(u.Href(false), "/")
	}
	return strings.TrimSuffix(value, "/")
}

type XaaConfig struct{ ClientID, ClientSecret, IDPClientID, IDPClientSecret, IDPIDToken, IDPTokenEndpoint string }

func (s *Service) CrossAppAccess(ctx context.Context, address string, config XaaConfig) (protocol.JSONObject, error) {
	resource, err := s.discoverResource(ctx, address, "")
	if err != nil {
		return protocol.JSONObject{}, fmt.Errorf("XAA: PRM discovery failed: %s", err.Error())
	}
	var servers []string
	_ = json.Unmarshal(resource.Get("authorization_servers"), &servers)
	if resource.String("resource") == "" || len(servers) == 0 || servers[0] == "" {
		return protocol.JSONObject{}, errors.New("XAA: PRM discovery failed: PRM missing resource or authorization_servers")
	}
	if normalizeURL(resource.String("resource")) != normalizeURL(address) {
		return protocol.JSONObject{}, fmt.Errorf("XAA: PRM discovery failed: PRM resource mismatch: expected %s, got %s", address, resource.String("resource"))
	}
	var metadata *protocol.JSONObject
	failures := []string{}
	for _, server := range servers {
		candidate, err := s.DiscoverAuthorizationServerMetadata(ctx, server)
		if err == nil && (candidate == nil || candidate.String("issuer") == "" || candidate.String("token_endpoint") == "") {
			err = fmt.Errorf("XAA: AS metadata discovery failed: no valid metadata at %s", server)
		}
		if err == nil && normalizeURL(candidate.String("issuer")) != normalizeURL(server) {
			err = fmt.Errorf("XAA: AS metadata discovery failed: issuer mismatch: expected %s, got %s", server, candidate.String("issuer"))
		}
		if err == nil {
			u, e := parseURL(candidate.String("token_endpoint"))
			if e != nil {
				err = e
			} else if u.Scheme() != "https" {
				err = fmt.Errorf("XAA: refusing non-HTTPS token endpoint: %s", candidate.String("token_endpoint"))
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return protocol.JSONObject{}, err
			}
			failures = append(failures, server+": "+err.Error())
			continue
		}
		if grants := candidate.Get("grant_types_supported"); grants != nil && !containsString(grants, jwtBearerGrant) {
			var values []string
			_ = json.Unmarshal(grants, &values)
			failures = append(failures, server+": does not advertise jwt-bearer grant (supported: "+strings.Join(values, ", ")+")")
			continue
		}
		metadata = candidate
		break
	}
	if metadata == nil {
		return protocol.JSONObject{}, errors.New("XAA: no authorization server supports jwt-bearer. Tried: " + strings.Join(failures, "; "))
	}
	method := "client_secret_basic"
	methods := metadata.Get("token_endpoint_auth_methods_supported")
	if methods != nil && !containsString(methods, "client_secret_basic") && containsString(methods, "client_secret_post") {
		method = "client_secret_post"
	}
	params := formPairs{{"grant_type", tokenExchangeGrant}, {"requested_token_type", idJAGType}, {"audience", metadata.String("issuer")}, {"resource", resource.String("resource")}, {"subject_token", config.IDPIDToken}, {"subject_token_type", idTokenType}, {"client_id", config.IDPClientID}}
	if config.IDPClientSecret != "" {
		params.set("client_secret", config.IDPClientSecret)
	}
	response, data, err := s.request(ctx, "POST", config.IDPTokenEndpoint, http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}, params.encode(), false)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return protocol.JSONObject{}, &XaaTokenExchangeError{fmt.Sprintf("XAA: token exchange failed: HTTP %d: %s", response.StatusCode, sliceUTF16(redactTokens(data), 200)), response.StatusCode < 500}
	}
	if !json.Valid(data) {
		return protocol.JSONObject{}, &XaaTokenExchangeError{"XAA: token exchange returned non-JSON (captive portal?) at " + config.IDPTokenEndpoint, false}
	}
	exchange := object(data)
	shapeOK := true
	for _, key := range []string{"access_token", "issued_token_type", "scope"} {
		if v := exchange.Get(key); v != nil && !isString(v) {
			shapeOK = false
		}
	}
	if v := exchange.Get("expires_in"); v != nil && !finite(number(v)) {
		shapeOK = false
	}
	if !shapeOK {
		return protocol.JSONObject{}, &XaaTokenExchangeError{"XAA: token exchange response did not match expected shape: " + redactParsedTokens(data), true}
	}
	parsedExchange := protocol.NewJSONObject()
	for _, key := range []string{"access_token", "issued_token_type", "expires_in", "scope"} {
		if v := exchange.Get(key); v != nil {
			if key == "expires_in" {
				v = jsonNumber(number(v))
			}
			parsedExchange.Set(key, v)
		}
	}
	exchange = parsedExchange
	if exchange.String("access_token") == "" {
		return protocol.JSONObject{}, &XaaTokenExchangeError{"XAA: token exchange response missing access_token: " + redactParsedTokens(raw(exchange)), true}
	}
	if exchange.String("issued_token_type") != idJAGType {
		value := exchange.String("issued_token_type")
		if exchange.Get("issued_token_type") == nil {
			value = "undefined"
		}
		return protocol.JSONObject{}, &XaaTokenExchangeError{"XAA: token exchange returned unexpected issued_token_type: " + value, true}
	}
	params = formPairs{{"grant_type", jwtBearerGrant}, {"assertion", exchange.String("access_token")}}
	headers := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}
	if method == "client_secret_basic" {
		headers.Set("Authorization", "Basic "+basicRevocation(config.ClientID, config.ClientSecret))
	} else {
		params.set("client_id", config.ClientID)
		params.set("client_secret", config.ClientSecret)
	}
	response, data, err = s.request(ctx, "POST", metadata.String("token_endpoint"), headers, params.encode(), false)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return protocol.JSONObject{}, fmt.Errorf("XAA: jwt-bearer grant failed: HTTP %d: %s", response.StatusCode, sliceUTF16(redactTokens(data), 200))
	}
	if !json.Valid(data) {
		return protocol.JSONObject{}, errors.New("XAA: jwt-bearer grant returned non-JSON (captive portal?) at " + metadata.String("token_endpoint"))
	}
	tokens := object(data)
	if tokens.Get("token_type") == nil {
		tokens.Set("token_type", quote("Bearer"))
	}
	tokens, err = validateTokens(tokens)
	if err != nil || tokens.String("access_token") == "" {
		return protocol.JSONObject{}, errors.New("XAA: jwt-bearer response did not match expected shape: " + redactParsedTokens(data))
	}
	tokens.Delete("id_token")
	tokens.Set("authorizationServerUrl", metadata.Get("issuer"))
	return tokens, nil
}
func (s *Service) authenticateXAA(ctx context.Context, name string, config protocol.JSONObject, onURL func(string) error, options FlowOptions) (flowErr error) {
	settings, err := s.options.Settings(ctx)
	if err != nil {
		return err
	}
	idp := object(settings.Get("xaaIdp"))
	if len(idp.OwnProperties()) == 0 {
		return errors.New("XAA: no IdP connection configured. Run 'claude mcp xaa setup --issuer <url> --client-id <id> --client-secret' to configure.")
	}
	clientID := object(config.Get("oauth")).String("clientId")
	if clientID == "" {
		return fmt.Errorf("XAA: server '%s' needs an AS client_id. Re-add with --client-id.", name)
	}
	client, err := s.ClientConfig(ctx, name, config)
	if err != nil {
		return err
	}
	secret := client.String("clientSecret")
	if secret == "" {
		return fmt.Errorf("XAA: AS client secret not found for '%s'. Re-add with --client-secret.", name)
	}
	idpSecret, err := s.IDPClientSecret(ctx, idp.String("issuer"))
	if err != nil {
		return err
	}
	cachedIDP, _ := s.CachedIDPIDToken(ctx, idp.String("issuer"))
	cacheHit := cachedIDP != ""
	stage := "idp_login"
	defer func() {
		fields := record(field("authMethod", "xaa"), field("idTokenCacheHit", cacheHit))
		if flowErr == nil {
			s.event("tengu_mcp_oauth_flow_success", fields)
			return
		}
		var cancelled AuthenticationCancelledError
		if errors.As(flowErr, &cancelled) {
			return
		}
		fields.Set("xaaFailureStage", quote(stage))
		s.event("tengu_mcp_oauth_flow_failure", fields)
	}()
	idToken, err := s.AcquireIDPIDToken(ctx, idp.String("issuer"), idp.String("clientId"), idpSecret, idp.Get("callbackPort"), onURL, options.SkipBrowserOpen)
	if err != nil {
		if ctx.Err() != nil {
			return AuthenticationCancelledError{}
		}
		return err
	}
	stage = "discovery"
	metadata, err := s.DiscoverOIDC(ctx, idp.String("issuer"))
	if err != nil {
		return err
	}
	stage = "token_exchange"
	tokens, err := s.CrossAppAccess(ctx, config.String("url"), XaaConfig{clientID, secret, idp.String("clientId"), idpSecret, idToken, metadata.String("token_endpoint")})
	if err != nil {
		var exchange *XaaTokenExchangeError
		if errors.As(err, &exchange) && exchange.ShouldClearIDToken {
			_ = s.ClearIDPIDToken(ctx, idp.String("issuer"))
		}
		if !errors.As(err, &exchange) {
			message := err.Error()
			if strings.Contains(message, "PRM discovery failed") || strings.Contains(message, "AS metadata discovery failed") || strings.Contains(message, "no authorization server supports jwt-bearer") {
				stage = "discovery"
			} else if strings.Contains(message, "jwt-bearer") {
				stage = "jwt_bearer"
			}
		}
		if ctx.Err() != nil {
			return AuthenticationCancelledError{}
		}
		return err
	}
	provider, err := s.Provider(ctx, name, config)
	if err != nil {
		return err
	}
	return provider.saveXAATokens(ctx, tokens, clientID, secret)
}
func (p *Provider) saveXAATokens(ctx context.Context, tokens protocol.JSONObject, clientID, secret string) error {
	return p.updateEntry(ctx, func(entry protocol.JSONObject) error {
		entry.Set("serverName", quote(p.name))
		entry.Set("serverUrl", p.config.Get("url"))
		entry.Set("accessToken", tokens.Get("access_token"))
		if v := tokens.Get("refresh_token"); v != nil && string(v) != "null" {
			entry.Set("refreshToken", v)
		}
		expires := number(tokens.Get("expires_in"))
		if !truthy(tokens.Get("expires_in")) {
			expires = 3600
		}
		entry.Set("expiresAt", jsonNumber(float64(p.service.options.Now().UnixMilli())+expires*1000))
		entry.Set("scope", tokens.Get("scope"))
		entry.Set("clientId", quote(clientID))
		entry.Set("clientSecret", quote(secret))
		entry.Set("discoveryState", raw(protocol.NewJSONObject(protocol.JSONField{Name: "authorizationServerUrl", Value: tokens.Get("authorizationServerUrl")})))
		return nil
	})
}
func (p *Provider) xaaRefresh(ctx context.Context) (*protocol.JSONObject, error) {
	settings, err := p.service.options.Settings(ctx)
	if err != nil {
		return nil, err
	}
	idp := object(settings.Get("xaaIdp"))
	if len(idp.OwnProperties()) == 0 {
		return nil, nil
	}
	token, err := p.service.CachedIDPIDToken(ctx, idp.String("issuer"))
	if err != nil || token == "" {
		return nil, err
	}
	clientID := object(p.config.Get("oauth")).String("clientId")
	client, err := p.service.ClientConfig(ctx, p.name, p.config)
	if err != nil {
		return nil, err
	}
	if clientID == "" || client.String("clientSecret") == "" {
		return nil, nil
	}
	secret, err := p.service.IDPClientSecret(ctx, idp.String("issuer"))
	if err != nil {
		return nil, err
	}
	metadata, err := p.service.DiscoverOIDC(ctx, idp.String("issuer"))
	if err != nil {
		return nil, nil
	}
	tokens, err := p.service.CrossAppAccess(ctx, p.config.String("url"), XaaConfig{clientID, client.String("clientSecret"), idp.String("clientId"), secret, token, metadata.String("token_endpoint")})
	if err != nil {
		var exchange *XaaTokenExchangeError
		if errors.As(err, &exchange) && exchange.ShouldClearIDToken {
			_ = p.service.ClearIDPIDToken(ctx, idp.String("issuer"))
		}
		return nil, err
	}
	if err = p.saveXAATokens(ctx, tokens, clientID, client.String("clientSecret")); err != nil {
		return nil, err
	}
	result := tokens.Clone()
	result.Set("token_type", quote("Bearer"))
	result.Delete("authorizationServerUrl")
	return &result, nil
}
