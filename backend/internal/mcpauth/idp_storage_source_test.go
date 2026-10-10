package mcpauth

import (
	"context"
	"encoding/base64"
	"testing"
	"time"
)

func TestIdPIssuerCacheTTLSecretSlotsAndDiscoveryClearAgainstSource(t *testing.T) {
	s, home := authFixture(t)
	now := int64(1700000000000)
	s.options.Now = func() time.Time { return time.UnixMilli(now) }
	config := record(field("type", "http"), field("url", "https://mcp.example"))
	key, _ := GetServerKey("cache", config)
	initial := record(field("future", true), field("mcpOAuth", map[string]any{key: map[string]any{"discoveryState": map[string]any{"authorizationServerUrl": "https://as.example"}, "future": "keep"}}))
	if result := s.options.Secure.Update(context.Background(), initial); !result.Success {
		t.Fatal(result)
	}
	issuer := "HTTPS://IdP.Example:443/tenant///?query=x#fragment"
	jwt := "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"exp":1700001000}`)) + ".signature"
	steps := []map[string]any{{"action": "issuer-key", "issuer": issuer}, {"action": "issuer-key", "issuer": "invalid///"}, {"action": "save-idp-token", "issuer": issuer, "value": jwt}, {"action": "idp-token", "issuer": issuer}, {"action": "advance", "delta": 940000}, {"action": "idp-token", "issuer": issuer}, {"action": "save-idp-token", "issuer": issuer, "value": "broken.token"}, {"action": "idp-token", "issuer": issuer}, {"action": "save-idp-secret", "issuer": issuer, "value": "owned-secret"}, {"action": "idp-secret", "issuer": issuer}, {"action": "clear-idp-secret", "issuer": issuer}, {"action": "clear-idp-secret", "issuer": issuer}, {"action": "clear-idp-token", "issuer": issuer}, {"action": "clear-idp-token", "issuer": issuer}, {"action": "has-discovery"}, {"action": "clear-discovery"}, {"action": "has-discovery"}, {"action": "clear-discovery"}}
	observations := []any{}
	for _, op := range steps {
		var value any
		var err error
		issuer, _ := op["issuer"].(string)
		switch op["action"] {
		case "issuer-key":
			value = IssuerKey(issuer)
		case "save-idp-token":
			value, err = s.SaveIDPIDTokenFromJWT(context.Background(), issuer, op["value"].(string))
		case "idp-token":
			var token string
			token, err = s.CachedIDPIDToken(context.Background(), issuer)
			if token != "" {
				value = token
			}
		case "advance":
			now += int64(op["delta"].(int))
		case "save-idp-secret":
			result, e := s.SaveIDPClientSecret(context.Background(), issuer, op["value"].(string))
			err = e
			if !result.Success {
				t.Fatal(result)
			}
		case "idp-secret":
			value, err = s.IDPClientSecret(context.Background(), issuer)
		case "clear-idp-secret":
			err = s.ClearIDPClientSecret(context.Background(), issuer)
		case "clear-idp-token":
			err = s.ClearIDPIDToken(context.Background(), issuer)
		case "has-discovery":
			value, err = s.HasDiscoveryWithoutToken(context.Background(), "cache", config)
		case "clear-discovery":
			err = s.ClearDiscoveryWithoutToken(context.Background(), "cache", config)
		}
		if err != nil {
			t.Fatal(err)
		}
		storage, e := s.read(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		observations = append(observations, map[string]any{"value": value, "storage": storage})
	}
	compareAuthSource(t, home, map[string]any{"name": "cache", "config": config, "now": 1700000000000, "initial": initial, "operations": steps}, observations)
}
