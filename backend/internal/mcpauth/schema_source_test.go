package mcpauth

import (
	"testing"
)

func TestMetadataTokensAndCallbackFilteringAgainstActualSDKSource(t *testing.T) {
	_, home := authFixture(t)
	type item struct {
		Kind  string `json:"kind"`
		Value any    `json:"value"`
	}
	cases := []item{
		{"oauth", map[string]any{"issuer": "identifier", "authorization_endpoint": "https://as.example/auth", "token_endpoint": "https://as.example/token", "response_types_supported": []any{}, "future_field": true, "jwks_uri": 42}},
		{"resource", map[string]any{"resource": "data:text/plain,x", "resource_documentation": "not a URL", "jwks_uri": "javascript:alert(1)", "authorization_endpoint": false}},
		{"resource", map[string]any{"resource": "https://resource.example", "authorization_servers": []any{"javascript:alert(1)"}}},
		{"oidc", map[string]any{"issuer": "identifier", "authorization_endpoint": "https://as.example/auth", "token_endpoint": "https://as.example/token", "jwks_uri": "https://as.example/jwks", "response_types_supported": []any{}, "subject_types_supported": []any{}, "id_token_signing_alg_values_supported": []any{}, "service_documentation": "not a URL", "future_field": true, "revocation_endpoint": false, "code_challenge_methods_supported": []any{"S256"}}},
		{"oauth", map[string]any{"issuer": "identifier", "authorization_endpoint": "data:x", "token_endpoint": "https://as.example/token", "response_types_supported": []any{}}},
		{"tokens", map[string]any{"access_token": "x", "token_type": "Bearer", "expires_in": nil, "future": true}},
		{"tokens", map[string]any{"access_token": "x", "token_type": "Bearer", "expires_in": false}},
		{"tokens", map[string]any{"access_token": "x", "token_type": "Bearer", "expires_in": " 600 "}},
		{"tokens", map[string]any{"access_token": "x", "token_type": "Bearer", "expires_in": "garbage"}},
	}
	cases = append(cases, item{"client", map[string]any{"redirect_uris": []any{"https://client.example/callback"}, "client_id": "registered", "logo_uri": "", "tos_uri": "", "jwks": nil, "unknown": true}}, item{"client", map[string]any{"redirect_uris": []any{"javascript:bad", "invalid"}, "client_id": 42}}, item{"oauth", map[string]any{}}, item{"oidc", map[string]any{"issuer": 42, "authorization_endpoint": "bad", "token_endpoint": "data:x", "response_types_supported": []any{nil, 42}}})
	for _, ttl := range []any{[]any{}, []any{"600"}, []any{nil}, []any{"1", "2"}, "0x12", "0o17", "0b101", "0x1_2", "Inf", "1e309", "\ufeff \ufeff 600 \ufeff"} {
		cases = append(cases, item{"tokens", map[string]any{"access_token": "x", "token_type": "Bearer", "expires_in": ttl}})
	}
	for _, text := range []string{"plain & text", "<script>alert(1)</script>", "<b>allowed</b><iframe src=x>oops</iframe>", "<a href=\"javascript:alert(1)\" title=\"x\">link</a>", "<img src=\"https://a.example/x\" onerror=\"bad\">", "<!--comment--><p>x</p>", "<a href=\"relative/file\">link</a>", "<script>if(a<b){evil()}</script>", "<p title=\"x\">hello</p>"} {
		cases = append(cases, item{"html", text})
	}
	results := []any{}
	for _, c := range cases {
		if c.Kind == "html" {
			results = append(results, map[string]any{"value": sanitizeCallbackHTML(c.Value.(string))})
			continue
		}
		value := object(raw(c.Value))
		parsed, err := validateMetadata(value, func() string {
			if c.Kind == "client" || c.Kind == "tokens" {
				return "oauth"
			}
			return c.Kind
		}())
		if c.Kind == "client" {
			parsed, err = validateClientInfo(value)
		}
		if c.Kind == "tokens" {
			parsed, err = validateTokens(value)
		}
		if err != nil {
			results = append(results, map[string]any{"invalid": true, "error": err.Error()})
		} else {
			results = append(results, map[string]any{"value": parsed})
		}
	}
	compareAuthSource(t, home, map[string]any{"mode": "schemas", "now": 1700000000000, "initial": map[string]any{}, "cases": cases}, results)
}
