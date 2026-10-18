package mcpauth

import (
	"context"
	"testing"
)

func TestMenuAuthenticationUsesActualSourceProviderTokensInsteadOfRawAccessPresence(t *testing.T) {
	for _, tc := range []struct {
		name, entry string
		present     bool
	}{
		{"absent", `null`, false},
		{"future-access", `{"accessToken":"private-menu-token","expiresAt":1700000900000}`, true},
		{"expired-access-without-refresh", `{"accessToken":"expired-private-token","expiresAt":1699999999000}`, false},
		{"future-empty-access-object", `{"accessToken":"","expiresAt":1700000900000}`, true},
		{"partial-registration", `{"accessToken":"","expiresAt":0,"clientId":"registered"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, home := authFixture(t)
			config := record(field("type", "http"), field("url", "http://127.0.0.1:1/mcp"))
			key, err := GetServerKey("menu", config)
			if err != nil {
				t.Fatal(err)
			}
			initial := authObject(t, `{"future":{"keep":true},"mcpOAuth":{}}`)
			if tc.entry != "null" {
				slots := object(initial.Get("mcpOAuth"))
				slots.Set(key, []byte(tc.entry))
				initial.Set("mcpOAuth", raw(slots))
			}
			if result := s.options.Secure.Update(context.Background(), initial); !result.Success {
				t.Fatal(result)
			}
			present, err := s.HasTokens(context.Background(), "menu", config)
			if err != nil || present != tc.present {
				t.Fatal("menu presence bypassed actual provider decision", present, err)
			}
			provider, err := s.Provider(context.Background(), "menu", config)
			if err != nil {
				t.Fatal(err)
			}
			tokens, err := provider.Tokens(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			stored, err := s.read(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			compareAuthSource(t, home, map[string]any{"name": "menu", "config": config, "now": 1700000000000, "initial": initial, "operations": []any{map[string]any{"action": "tokens"}}}, []any{map[string]any{"value": tokens, "storage": stored}})
		})
	}
}
