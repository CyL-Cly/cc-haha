package mcpauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestExplicitAndSilentXAAUseActualSettingsCredentialsAndTLSAgainstSource(t *testing.T) {
	for _, kind := range []string{"authenticate", "silent", "missing-idp", "missing-secret", "bad-id-token", "idp-outage"} {
		t.Run(kind, func(t *testing.T) {
			s, home := authFixture(t)
			s.options.Environment["CLAUDE_CODE_ENABLE_XAA"] = "1"
			events := []any{}
			s.options.OnEvent = func(name string, fields protocol.JSONObject) {
				events = append(events, map[string]any{"name": name, "fields": fields})
			}
			var server *httptest.Server
			var mu sync.Mutex
			requests := []oauthRequest{}
			server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				requests = append(requests, oauthRequest{r.Method, r.URL.RequestURI(), string(body), r.Header.Get("Authorization")})
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/idp/.well-known/openid-configuration":
					_ = json.NewEncoder(w).Encode(map[string]any{"issuer": server.URL + "/idp", "authorization_endpoint": server.URL + "/authorize", "token_endpoint": server.URL + "/idp-token", "jwks_uri": server.URL + "/jwks", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}})
				case "/.well-known/oauth-protected-resource/mcp":
					_ = json.NewEncoder(w).Encode(map[string]any{"resource": server.URL + "/mcp", "authorization_servers": []string{server.URL + "/as"}})
				case "/.well-known/oauth-authorization-server/as":
					_ = json.NewEncoder(w).Encode(map[string]any{"issuer": server.URL + "/as", "authorization_endpoint": server.URL + "/authorize", "token_endpoint": server.URL + "/as-token", "response_types_supported": []string{"code"}})
				case "/idp-token":
					if kind == "bad-id-token" || kind == "idp-outage" {
						status := 400
						if kind == "idp-outage" {
							status = 500
						}
						w.WriteHeader(status)
						_, _ = w.Write([]byte(`{"error":"invalid_grant","subject_token":"secret"}`))
					} else {
						_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "jag-secret", "issued_token_type": idJAGType})
					}
				case "/as-token":
					_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "xaa-access", "token_type": "custom-type", "expires_in": 600, "scope": "read"})
				default:
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			s.options.HTTPClient = server.Client()
			config := record(field("type", "http"), field("url", server.URL+"/mcp"), field("oauth", map[string]any{"xaa": true, "clientId": "as-client"}))
			issuer := server.URL + "/idp"
			settings := record(field("xaaIdp", map[string]any{"issuer": issuer, "clientId": "idp-client"}))
			s.options.Settings = func(context.Context) (protocol.JSONObject, error) { return settings.Clone(), nil }
			key, _ := GetServerKey("xaa-source", config)
			initial := record(field("future", true), field("mcpXaaIdp", map[string]any{IssuerKey(issuer): map[string]any{"idToken": "idp-token", "expiresAt": 1700001000000}}), field("mcpXaaIdpConfig", map[string]any{IssuerKey(issuer): map[string]any{"clientSecret": "idp-secret"}}), field("mcpOAuthClientConfig", map[string]any{key: map[string]any{"clientSecret": "as-secret"}}))
			if kind == "missing-idp" {
				initial.Delete("mcpXaaIdp")
			}
			if kind == "missing-secret" {
				initial.Delete("mcpOAuthClientConfig")
			}
			if result := s.options.Secure.Update(context.Background(), initial); !result.Success {
				t.Fatal(result)
			}
			action := "tokens"
			var value, errorValue any
			var err error
			if kind == "authenticate" {
				action = "authenticate"
				err = s.Authenticate(context.Background(), "xaa-source", config, func(string) error { t.Error("cached IdP opened browser"); return nil })
			} else {
				p, e := s.Provider(context.Background(), "xaa-source", config)
				if e != nil {
					t.Fatal(e)
				}
				value, err = p.Tokens(context.Background())
			}
			if err != nil {
				errorValue = err.Error()
			}
			storage, e := s.read(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			mu.Lock()
			nativeRequests := append([]oauthRequest{}, requests...)
			requests = nil
			mu.Unlock()
			compareAuthSource(t, home, map[string]any{"mode": "fullxaa", "captureEvents": true, "action": action, "name": "xaa-source", "config": config, "settings": settings, "now": 1700000000000, "initial": initial}, map[string]any{"value": value, "error": errorValue, "storage": storage, "events": events})
			mu.Lock()
			sourceRequests := append([]oauthRequest{}, requests...)
			mu.Unlock()
			if len(sourceRequests) > 0 && !reflect.DeepEqual(nativeRequests, sourceRequests) {
				t.Fatalf("actual XAA auth HTTP sequence differs\nGo %#v\nSource %#v", nativeRequests, sourceRequests)
			}
		})
	}
}
