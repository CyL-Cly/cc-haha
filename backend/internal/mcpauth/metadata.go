package mcpauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	weburl "github.com/nlnwa/whatwg-url/url"
)

func parseURL(value string) (*weburl.Url, error) { return weburl.Parse(value) }
func origin(u *weburl.Url) string {
	switch u.Scheme() {
	case "http", "https", "ws", "wss", "ftp":
		return u.Scheme() + "://" + u.Host()
	}
	return "null"
}
func (s *Service) request(ctx context.Context, method, address string, headers http.Header, body []byte, normalizePost bool) (*http.Response, []byte, error) {
	u, err := parseURL(address)
	if err != nil {
		return nil, nil, err
	}
	if u.Username() != "" || u.Password() != "" {
		return nil, nil, errors.New("Request cannot include credentials in URL.")
	}
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, u.Href(false), bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	request.Header = headers.Clone()
	response, err := s.options.HTTPClient.Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, nil, err
	}
	data = protocol.DecodeJavaScriptUTF8(data)
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if normalizePost && method == "POST" && response.StatusCode >= 200 && response.StatusCode <= 299 {
		status, normalized := normalizeOAuthErrorBody(response.StatusCode, data)
		response.StatusCode = status
		data = normalized
	}
	return response, data, nil
}
func normalizeOAuthErrorBody(status int, body []byte) (int, []byte) {
	if status < 200 || status >= 300 {
		return status, body
	}
	value := object(body)
	if _, err := validateTokens(value); err == nil {
		return status, body
	}
	if !isString(value.Get("error")) {
		return status, body
	}
	for _, key := range []string{"error_description", "error_uri"} {
		if raw := value.Get(key); raw != nil && !isString(raw) {
			return status, body
		}
	}
	code := value.String("error")
	description := value.Get("error_description")
	switch code {
	case "invalid_refresh_token", "expired_refresh_token", "token_expired":
		if description == nil || string(description) == "null" {
			description = quote("Server returned non-standard error code: " + code)
		}
		code = "invalid_grant"
	}
	value = protocol.NewJSONObject(field("error", code), protocol.JSONField{Name: "error_description", Value: description}, protocol.JSONField{Name: "error_uri", Value: value.Get("error_uri")})
	data, _ := value.MarshalJSON()
	return 400, data
}
func parseOAuthError(status int, body []byte) error {
	value := object(body)
	if isString(value.Get("error")) {
		valid := true
		for _, key := range []string{"error_description", "error_uri"} {
			if v := value.Get(key); v != nil && !isString(v) {
				valid = false
			}
		}
		if valid {
			code := value.String("error")
			switch code {
			case "invalid_request", "invalid_client", "invalid_grant", "unauthorized_client", "unsupported_grant_type", "invalid_scope", "access_denied", "server_error", "temporarily_unavailable", "unsupported_response_type", "unsupported_token_type", "invalid_token", "method_not_allowed", "too_many_requests", "invalid_client_metadata", "insufficient_scope", "invalid_target":
			default:
				code = "server_error"
			}
			return &OAuthError{Code: code, Message: value.String("error_description"), URI: value.String("error_uri"), Status: status}
		}
	}
	return &OAuthError{Code: "server_error", Message: fmt.Sprintf("HTTP %d: Invalid OAuth error response. Raw body: %s", status, body), Status: status}
}
func validateTokens(value protocol.JSONObject) (protocol.JSONObject, error) {
	if issues := tokenIssues(value); len(issues) > 0 {
		return protocol.JSONObject{}, schemaError(issues)
	}
	result := protocol.NewJSONObject()
	for _, key := range []string{"access_token", "id_token", "token_type", "expires_in", "scope", "refresh_token"} {
		v := value.Get(key)
		if v == nil {
			continue
		}
		if key == "expires_in" {
			number := number(v)
			if !finite(number) {
				return protocol.JSONObject{}, errors.New("Invalid OAuth token expires_in")
			}
			result.Set(key, jsonNumber(number))
		} else {
			if !isString(v) {
				return protocol.JSONObject{}, fmt.Errorf("Invalid OAuth token %s", key)
			}
			result.Set(key, v)
		}
	}
	return result, nil
}

type metadataField struct {
	name, kind string
	required   bool
}

func validateMetadata(value protocol.JSONObject, kind string) (protocol.JSONObject, error) {
	if !value.IsObject() {
		return protocol.JSONObject{}, errors.New("Invalid OAuth metadata response")
	}
	fields := []metadataField{}
	add := func(names, typ string, required bool) {
		for _, name := range strings.Fields(names) {
			fields = append(fields, metadataField{name, typ, required})
		}
	}
	if kind == "resource" {
		add("resource", "url", true)
		add("authorization_servers", "safeurls", false)
		add("jwks_uri resource_policy_uri resource_tos_uri", "url", false)
		add("scopes_supported bearer_methods_supported resource_signing_alg_values_supported authorization_details_types_supported dpop_signing_alg_values_supported", "strings", false)
		add("resource_name resource_documentation", "string", false)
		add("tls_client_certificate_bound_access_tokens dpop_bound_access_tokens_required", "bool", false)
	} else {
		add("issuer", "string", true)
		add("authorization_endpoint token_endpoint", "safeurl", true)
		add("registration_endpoint", "safeurl", false)
		add("response_types_supported", "strings", true)
		add("scopes_supported response_modes_supported grant_types_supported token_endpoint_auth_methods_supported token_endpoint_auth_signing_alg_values_supported code_challenge_methods_supported", "strings", false)
		add("client_id_metadata_document_supported", "bool", false)
		if kind == "oidc" {
			add("jwks_uri", "safeurl", true)
			add("subject_types_supported id_token_signing_alg_values_supported", "strings", true)
			add("userinfo_endpoint op_policy_uri op_tos_uri", "safeurl", false)
			add("acr_values_supported id_token_encryption_alg_values_supported id_token_encryption_enc_values_supported userinfo_signing_alg_values_supported userinfo_encryption_alg_values_supported userinfo_encryption_enc_values_supported request_object_signing_alg_values_supported request_object_encryption_alg_values_supported request_object_encryption_enc_values_supported display_values_supported claim_types_supported claims_supported claims_locales_supported ui_locales_supported", "strings", false)
			add("service_documentation", "string", false)
			add("claims_parameter_supported request_parameter_supported request_uri_parameter_supported require_request_uri_registration", "bool", false)
		} else {
			add("service_documentation revocation_endpoint", "safeurl", false)
			add("revocation_endpoint_auth_methods_supported revocation_endpoint_auth_signing_alg_values_supported introspection_endpoint_auth_methods_supported introspection_endpoint_auth_signing_alg_values_supported", "strings", false)
			add("introspection_endpoint", "string", false)
		}
	}
	if issues := metadataIssues(value, kind, fields); len(issues) > 0 {
		return protocol.JSONObject{}, schemaError(issues)
	}
	result := value.Clone()
	if kind == "oidc" {
		result = protocol.NewJSONObject()
	}
	if kind == "oidc" {
		for _, name := range metadataOrder(kind) {
			if v := value.Get(name); v != nil {
				result.Set(name, v)
			}
		}
	}
	return result, nil
}
func (s *Service) DiscoverAuthorizationServerMetadata(ctx context.Context, address string) (*protocol.JSONObject, error) {
	u, err := parseURL(address)
	if err != nil {
		return nil, err
	}
	pathname := u.Pathname()
	if strings.HasSuffix(pathname, "/") {
		pathname = pathname[:len(pathname)-1]
	}
	paths := []string{"/.well-known/oauth-authorization-server" + pathname, "/.well-known/openid-configuration" + pathname}
	if pathname != "" {
		paths = append(paths, pathname+"/.well-known/openid-configuration")
	}
	for i, path := range paths {
		response, data, err := s.request(ctx, "GET", origin(u)+path, http.Header{"Mcp-Protocol-Version": {mcpclient.LatestProtocolVersion}, "Accept": {"application/json"}}, nil, false)
		if err != nil {
			return nil, err
		}
		kind := "OAuth"
		schema := "oauth"
		if i > 0 {
			kind = "OpenID provider"
			schema = "oidc"
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			if response.StatusCode >= 400 && response.StatusCode < 500 {
				continue
			}
			return nil, fmt.Errorf("HTTP %d trying to load %s metadata from %s", response.StatusCode, kind, origin(u)+path)
		}
		value, err := validateMetadata(object(data), schema)
		if err != nil {
			return nil, err
		}
		return &value, nil
	}
	return nil, nil
}
func (s *Service) discoverResource(ctx context.Context, address, override string) (*protocol.JSONObject, error) {
	u, err := parseURL(address)
	if err != nil {
		return nil, err
	}
	path := strings.TrimSuffix(u.Pathname(), "/")
	endpoint := origin(u) + "/.well-known/oauth-protected-resource" + path + u.Search()
	if override != "" {
		endpoint = override
	}
	response, data, err := s.request(ctx, "GET", endpoint, http.Header{"Mcp-Protocol-Version": {mcpclient.LatestProtocolVersion}}, nil, false)
	if err != nil {
		return nil, err
	}
	if override == "" && response.StatusCode >= 400 && response.StatusCode < 500 && u.Pathname() != "/" {
		endpoint = origin(u) + "/.well-known/oauth-protected-resource"
		response, data, err = s.request(ctx, "GET", endpoint, http.Header{"Mcp-Protocol-Version": {mcpclient.LatestProtocolVersion}}, nil, false)
		if err != nil {
			return nil, err
		}
	}
	if response.StatusCode == 404 {
		return nil, errors.New("Resource server does not implement OAuth 2.0 Protected Resource Metadata.")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d trying to load well-known OAuth protected resource metadata.", response.StatusCode)
	}
	value, err := validateMetadata(object(data), "resource")
	if err != nil {
		return nil, err
	}
	return &value, nil
}

type serverInfo struct {
	authorizationURL   string
	metadata, resource *protocol.JSONObject
}

func (s *Service) discoverServerInfo(ctx context.Context, address, resourceURL string) (serverInfo, error) {
	u, err := parseURL(address)
	if err != nil {
		return serverInfo{}, err
	}
	result := serverInfo{authorizationURL: origin(u) + "/"}
	if resource, err := s.discoverResource(ctx, address, resourceURL); err == nil {
		result.resource = resource
		var servers []string
		if json.Unmarshal(resource.Get("authorization_servers"), &servers) == nil && len(servers) > 0 {
			result.authorizationURL = servers[0]
		}
	}
	result.metadata, err = s.DiscoverAuthorizationServerMetadata(ctx, result.authorizationURL)
	return result, err
}
func (s *Service) fetchAuthMetadata(ctx context.Context, name, address, hint, resourceURL string) (*protocol.JSONObject, error) {
	if hint != "" {
		if !strings.HasPrefix(hint, "https://") {
			return nil, fmt.Errorf("authServerMetadataUrl must use https:// (got: %s)", hint)
		}
		response, data, err := s.request(ctx, "GET", hint, http.Header{"Accept": {"application/json"}}, nil, false)
		if err != nil {
			return nil, err
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, fmt.Errorf("HTTP %d fetching configured auth server metadata from %s", response.StatusCode, hint)
		}
		value, err := validateMetadata(object(data), "oauth")
		if err != nil {
			return nil, err
		}
		return &value, nil
	}
	if info, err := s.discoverServerInfo(ctx, address, resourceURL); err == nil && info.metadata != nil {
		return info.metadata, nil
	}
	u, err := parseURL(address)
	if err != nil {
		return nil, err
	}
	if u.Pathname() == "/" {
		return nil, nil
	}
	return s.DiscoverAuthorizationServerMetadata(ctx, address)
}
