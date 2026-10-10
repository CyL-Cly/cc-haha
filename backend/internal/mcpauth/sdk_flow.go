package mcpauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	weburl "github.com/nlnwa/whatwg-url/url"
)

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

type authOptions struct {
	authorizationCode          *string
	scope, resourceMetadataURL string
}
type challengeParams struct{ scope, resource string }

var wwwScope = regexp.MustCompile(`scope=(?:"([^"]+)"|([^\s,]+))`)
var wwwResource = regexp.MustCompile(`resource_metadata=(?:"([^"]+)"|([^\s,]+))`)

func wwwAuthenticate(header string) challengeParams {
	parts := strings.Split(header, " ")
	if len(parts) < 2 || strings.ToLower(parts[0]) != "bearer" || parts[1] == "" {
		return challengeParams{}
	}
	field := func(pattern *regexp.Regexp) string {
		match := pattern.FindStringSubmatch(header)
		if match == nil {
			return ""
		}
		if match[1] != "" {
			return match[1]
		}
		return match[2]
	}
	scope, resource := field(wwwScope), field(wwwResource)
	if resource != "" {
		if u, err := parseURL(resource); err == nil {
			resource = u.Href(false)
		} else {
			resource = ""
		}
	}
	return challengeParams{scope, resource}
}
func (s *Service) auth(ctx context.Context, p *Provider, options authOptions) (string, error) {
	result, err := s.authInternal(ctx, p, options)
	var oauth *OAuthError
	if errors.As(err, &oauth) {
		scope := ""
		switch oauth.Code {
		case "invalid_client", "unauthorized_client":
			scope = "all"
		case "invalid_grant":
			scope = "tokens"
		}
		if scope != "" {
			if err := p.InvalidateCredentials(ctx, scope); err != nil {
				return "", err
			}
			return s.authInternal(ctx, p, options)
		}
	}
	return result, err
}
func (s *Service) authInternal(ctx context.Context, p *Provider, options authOptions) (string, error) {
	cached, err := p.DiscoveryState(ctx)
	if err != nil {
		return "", err
	}
	resourceURL := options.resourceMetadataURL
	if resourceURL == "" && cached != nil {
		resourceURL = cached.String("resourceMetadataUrl")
		if resourceURL != "" {
			if _, err := parseURL(resourceURL); err != nil {
				return "", err
			}
		}
	}
	var info serverInfo
	if cached != nil && truthy(cached.Get("authorizationServerUrl")) {
		info.authorizationURL = cached.String("authorizationServerUrl")
		if metadata := cached.Get("authorizationServerMetadata"); metadata != nil && truthy(metadata) {
			value := object(metadata)
			info.metadata = &value
		} else {
			info.metadata, err = s.DiscoverAuthorizationServerMetadata(ctx, info.authorizationURL)
			if err != nil {
				return "", err
			}
		}
		if resource := cached.Get("resourceMetadata"); resource != nil && truthy(resource) {
			value := object(resource)
			info.resource = &value
		} else {
			info.resource, _ = s.discoverResource(ctx, p.config.String("url"), resourceURL)
		}
	} else {
		info, err = s.discoverServerInfo(ctx, p.config.String("url"), resourceURL)
		if err != nil {
			return "", err
		}
	}
	state := record(field("authorizationServerUrl", info.authorizationURL))
	if resourceURL != "" {
		state.Set("resourceMetadataUrl", quote(resourceURL))
	}
	if info.metadata != nil {
		state.Set("authorizationServerMetadata", raw(*info.metadata))
	}
	if info.resource != nil {
		state.Set("resourceMetadata", raw(*info.resource))
	}
	if err = p.SaveDiscoveryState(ctx, state); err != nil {
		return "", err
	}
	resource, err := selectResource(p.config.String("url"), info.resource)
	if err != nil {
		return "", err
	}
	scope := options.scope
	if scope == "" && info.resource != nil {
		var scopes []string
		if json.Unmarshal(info.resource.Get("scopes_supported"), &scopes) == nil {
			scope = strings.Join(scopes, " ")
		}
	}
	if scope == "" {
		scope = p.ClientMetadata().String("scope")
	}
	client, err := p.ClientInformation(ctx)
	if err != nil {
		return "", err
	}
	if client == nil {
		if options.authorizationCode != nil {
			return "", errors.New("Existing OAuth client information is required when exchanging an authorization code")
		}
		metadataURL := p.ClientMetadataURL()
		if metadataURL != "" {
			u, err := parseURL(metadataURL)
			if err != nil || u.Scheme() != "https" || u.Pathname() == "/" {
				return "", &OAuthError{Code: "invalid_client_metadata", Message: "clientMetadataUrl must be a valid HTTPS URL with a non-root pathname, got: " + metadataURL}
			}
		}
		if info.metadata != nil && string(info.metadata.Get("client_id_metadata_document_supported")) == "true" && metadataURL != "" {
			value := record(field("client_id", metadataURL))
			client = &value
		} else {
			value, err := s.registerClient(ctx, info.authorizationURL, info.metadata, p.ClientMetadata(), scope)
			if err != nil {
				return "", err
			}
			client = &value
		}
		if err = p.SaveClientInformation(ctx, *client); err != nil {
			return "", err
		}
	}
	if options.authorizationCode != nil {
		if *options.authorizationCode == "" {
			return "", errors.New("Either provider.prepareTokenRequest() or authorizationCode is required")
		}
		if p.redirect == "" {
			return "", errors.New("redirectUrl is required for authorization_code flow")
		}
		verifier, err := p.CodeVerifier()
		if err != nil {
			return "", err
		}
		client, err = p.ClientInformation(ctx)
		if err != nil {
			return "", err
		}
		tokens, err := s.tokenRequest(ctx, info.authorizationURL, info.metadata, client, formPairs{{"grant_type", "authorization_code"}, {"code", *options.authorizationCode}, {"code_verifier", verifier}, {"redirect_uri", p.redirect}}, resource, false)
		if err != nil {
			return "", err
		}
		if err = p.SaveTokens(ctx, tokens); err != nil {
			return "", err
		}
		return "AUTHORIZED", nil
	}
	tokens, err := p.Tokens(ctx)
	if err != nil {
		return "", err
	}
	if tokens != nil && truthy(tokens.Get("refresh_token")) {
		newTokens, err := s.refreshRequest(ctx, info.authorizationURL, info.metadata, client, tokens.String("refresh_token"), resource, false)
		if err == nil {
			if err = p.SaveTokens(ctx, newTokens); err != nil {
				return "", err
			}
			return "AUTHORIZED", nil
		}
		var oauth *OAuthError
		if errors.As(err, &oauth) && oauth.Code != "server_error" {
			return "", err
		}
	}
	if info.metadata != nil {
		if !containsString(info.metadata.Get("response_types_supported"), "code") {
			return "", errors.New("Incompatible auth server: does not support response type code")
		}
		if raw := info.metadata.Get("code_challenge_methods_supported"); raw != nil && !containsString(raw, "S256") {
			return "", errors.New("Incompatible auth server: does not support code challenge method S256")
		}
	}
	endpoint := info.authorizationURL + "authorize"
	if info.metadata != nil {
		endpoint = info.metadata.String("authorization_endpoint")
	} else {
		u, err := parseURL(info.authorizationURL)
		if err != nil {
			return "", err
		}
		endpoint = origin(u) + "/authorize"
	}
	authorization, err := parseURL(endpoint)
	if err != nil {
		return "", err
	}
	stateValue, err := p.State()
	if err != nil {
		return "", err
	}
	verifier, challenge, err := p.pkce()
	if err != nil {
		return "", err
	}
	query := authorization.SearchParams()
	query.Set("response_type", "code")
	query.Set("client_id", client.String("client_id"))
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	query.Set("redirect_uri", p.redirect)
	if stateValue != "" {
		query.Set("state", stateValue)
	}
	if scope != "" {
		query.Set("scope", scope)
	}
	if strings.Contains(scope, "offline_access") {
		query.Append("prompt", "consent")
	}
	if resource != "" {
		query.Set("resource", resource)
	}
	encodeSearchParams(authorization)
	p.SaveCodeVerifier(verifier)
	if err = p.RedirectToAuthorization(ctx, authorization.Href(false)); err != nil {
		return "", err
	}
	return "REDIRECT", nil
}

// The URL parser's default query encoder permits ':' and '/'; URLSearchParams
// in the original SDK always uses the application/x-www-form-urlencoded set.
func encodeSearchParams(u *weburl.Url) {
	pairs := formPairs{}
	u.SearchParams().Iterate(func(pair *weburl.NameValuePair) { pairs = append(pairs, formPair{pair.Name, pair.Value}) })
	u.SetSearch(string(pairs.encode()))
}
func containsString(raw json.RawMessage, value string) bool {
	var values []string
	if json.Unmarshal(raw, &values) != nil {
		return false
	}
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}
func selectResource(address string, metadata *protocol.JSONObject) (string, error) {
	u, err := parseURL(address)
	if err != nil {
		return "", err
	}
	u.SetHash("")
	if metadata == nil {
		return "", nil
	}
	configured, err := parseURL(metadata.String("resource"))
	if err != nil {
		return "", err
	}
	requestedPath, configuredPath := u.Pathname(), configured.Pathname()
	allowed := origin(u) == origin(configured) && len(requestedPath) >= len(configuredPath) && strings.HasPrefix(strings.TrimSuffix(requestedPath, "/")+"/", strings.TrimSuffix(configuredPath, "/")+"/")
	if !allowed {
		return "", fmt.Errorf("Protected resource %s does not match expected %s (or origin)", metadata.String("resource"), u.Href(false))
	}
	return configured.Href(false), nil
}

type formPair struct{ key, value string }
type formPairs []formPair

func (form formPairs) encode() []byte {
	var out strings.Builder
	for i, pair := range form {
		if i > 0 {
			out.WriteByte('&')
		}
		out.WriteString(formEncode(pair.key))
		out.WriteByte('=')
		out.WriteString(formEncode(pair.value))
	}
	return []byte(out.String())
}
func (form *formPairs) set(key, value string) {
	for i, pair := range *form {
		if pair.key == key {
			(*form)[i].value = value
			return
		}
	}
	*form = append(*form, formPair{key, value})
}
func formEncode(value string) string {
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for _, b := range []byte(value) {
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.ContainsRune("*-._", rune(b)) {
			out.WriteByte(b)
		} else if b == ' ' {
			out.WriteByte('+')
		} else {
			out.WriteByte('%')
			out.WriteByte(hex[b>>4])
			out.WriteByte(hex[b&15])
		}
	}
	return out.String()
}
func (s *Service) registerClient(ctx context.Context, address string, metadata *protocol.JSONObject, client protocol.JSONObject, scope string) (protocol.JSONObject, error) {
	u, err := parseURL(address)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	endpoint := origin(u) + "/register"
	if metadata != nil {
		endpoint = metadata.String("registration_endpoint")
		if endpoint == "" {
			return protocol.JSONObject{}, errors.New("Incompatible auth server: does not support dynamic client registration")
		}
	}
	if scope != "" {
		client.Set("scope", quote(scope))
	}
	body, err := client.MarshalJSON()
	if err != nil {
		return protocol.JSONObject{}, err
	}
	response, data, err := s.request(ctx, "POST", endpoint, http.Header{"Content-Type": {"application/json"}}, body, false)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return protocol.JSONObject{}, parseOAuthError(response.StatusCode, data)
	}
	return validateClientInfo(object(data))
}
func clientAuthMethod(client *protocol.JSONObject, metadata *protocol.JSONObject) string {
	methods := []string{}
	if metadata != nil {
		_ = json.Unmarshal(metadata.Get("token_endpoint_auth_methods_supported"), &methods)
	}
	hasSecret := client != nil && client.Get("client_secret") != nil
	if client != nil {
		hint := client.String("token_endpoint_auth_method")
		if hint == "client_secret_basic" || hint == "client_secret_post" || hint == "none" {
			if len(methods) == 0 {
				return hint
			}
			for _, method := range methods {
				if hint == method {
					return hint
				}
			}
		}
	}
	if len(methods) == 0 {
		if hasSecret {
			return "client_secret_basic"
		}
		return "none"
	}
	for _, wanted := range []string{"client_secret_basic", "client_secret_post", "none"} {
		for _, method := range methods {
			if wanted == method && (wanted == "none" || hasSecret) {
				return wanted
			}
		}
	}
	if hasSecret {
		return "client_secret_post"
	}
	return "none"
}
func (s *Service) tokenRequest(ctx context.Context, address string, metadata *protocol.JSONObject, client *protocol.JSONObject, params formPairs, resource string, normalize bool) (protocol.JSONObject, error) {
	u, err := parseURL(address)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	endpoint := origin(u) + "/token"
	if metadata != nil && metadata.String("token_endpoint") != "" {
		endpoint = metadata.String("token_endpoint")
	}
	headers := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}, "Accept": {"application/json"}}
	if resource != "" {
		params.set("resource", resource)
	}
	if client != nil {
		switch clientAuthMethod(client, metadata) {
		case "client_secret_basic":
			if !truthy(client.Get("client_secret")) {
				return protocol.JSONObject{}, errors.New("client_secret_basic authentication requires a client_secret")
			}
			text := client.String("client_id") + ":" + client.String("client_secret")
			latin := []byte{}
			for _, r := range text {
				if r > 255 {
					return protocol.JSONObject{}, errors.New("Invalid character")
				}
				latin = append(latin, byte(r))
			}
			headers.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString(latin))
		case "client_secret_post":
			params.set("client_id", client.String("client_id"))
			if truthy(client.Get("client_secret")) {
				params.set("client_secret", client.String("client_secret"))
			}
		case "none":
			params.set("client_id", client.String("client_id"))
		}
	}
	response, data, err := s.request(ctx, "POST", endpoint, headers, params.encode(), normalize)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return protocol.JSONObject{}, parseOAuthError(response.StatusCode, data)
	}
	return validateTokens(object(data))
}
func (s *Service) refreshRequest(ctx context.Context, address string, metadata *protocol.JSONObject, client *protocol.JSONObject, refresh, resource string, normalize bool) (protocol.JSONObject, error) {
	tokens, err := s.tokenRequest(ctx, address, metadata, client, formPairs{{"grant_type", "refresh_token"}, {"refresh_token", refresh}}, resource, normalize)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	result := record(field("refresh_token", refresh))
	result.Assign(tokens)
	return result, nil
}
