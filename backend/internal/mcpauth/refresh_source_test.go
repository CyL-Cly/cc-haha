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
)

func TestRefreshAndRevocationActualProviderHTTPAgainstSource(t *testing.T) {
	for _, kind := range []string{"rotated", "missing-refresh", "invalid-grant", "alias-grant", "transient", "transient-exhausted", "no-client", "metadata-missing", "revoke-basic", "revoke-post", "revoke-bearer", "revoke-preserve"} {
		t.Run(kind, func(t *testing.T) {
			s, home := authFixture(t)
			var host *httptest.Server
			var mu sync.Mutex
			requests := []oauthRequest{}
			tokenCalls := 0
			host = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				requests = append(requests, oauthRequest{r.Method, r.URL.RequestURI(), string(body), r.Header.Get("Authorization")})
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/.well-known/oauth-authorization-server":
					if kind == "metadata-missing" {
						w.WriteHeader(404)
						return
					}
					metadata := map[string]any{"issuer": host.URL, "authorization_endpoint": host.URL + "/authorize", "token_endpoint": host.URL + "/token", "response_types_supported": []string{"code"}, "revocation_endpoint": host.URL + "/revoke"}
					if kind == "revoke-post" {
						metadata["revocation_endpoint_auth_methods_supported"] = []string{"client_secret_post"}
					}
					_ = json.NewEncoder(w).Encode(metadata)
				case "/token":
					tokenCalls++
					switch kind {
					case "invalid-grant":
						w.WriteHeader(400)
						_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"expired"}`))
					case "alias-grant":
						_, _ = w.Write([]byte(`{"error":"invalid_refresh_token"}`))
					case "transient", "transient-exhausted":
						if tokenCalls < 3 || kind == "transient-exhausted" {
							w.WriteHeader(503)
							_, _ = w.Write([]byte(`{"error":"temporarily_unavailable"}`))
							return
						}
						fallthrough
					default:
						tokens := map[string]any{"access_token": "renewed-access", "token_type": "Bearer", "expires_in": 3600, "scope": "read"}
						if kind != "missing-refresh" {
							tokens["refresh_token"] = "renewed-refresh"
						}
						_ = json.NewEncoder(w).Encode(tokens)
					}
				case "/revoke":
					if kind == "revoke-bearer" && r.Header.Get("Authorization") != "Bearer expired-access" {
						w.WriteHeader(401)
					} else {
						w.WriteHeader(200)
					}
				default:
					w.WriteHeader(404)
				}
			}))
			defer host.Close()
			config := record(field("type", "http"), field("url", host.URL+"/mcp?query=x#fragment"))
			key, _ := GetServerKey("refresh-source", config)
			entry := map[string]any{"serverName": "refresh-source", "serverUrl": config.String("url"), "accessToken": "expired-access", "refreshToken": "stale-refresh", "expiresAt": 1700000001000, "scope": "old", "discoveryState": map[string]any{"authorizationServerUrl": host.URL}, "stepUpScope": "read admin"}
			if kind != "no-client" {
				entry["clientId"] = "client:/ü"
				entry["clientSecret"] = "secret:/ü"
			}
			initial := record(field("future", true), field("mcpOAuth", map[string]any{key: entry, "unrelated": map[string]any{"accessToken": "keep"}}))
			if result := s.options.Secure.Update(context.Background(), initial); !result.Success {
				t.Fatal(result)
			}
			provider, e := s.Provider(context.Background(), "refresh-source", config)
			if e != nil {
				t.Fatal(e)
			}
			var value any
			action := "tokens"
			preserve := false
			if len(kind) > 6 && kind[:6] == "revoke" {
				action = "revoke"
				preserve = kind == "revoke-preserve"
				e = provider.Revoke(context.Background(), preserve)
			} else {
				value, e = provider.Tokens(context.Background())
			}
			if e != nil {
				t.Fatal(e)
			}
			storage, e := s.read(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			mu.Lock()
			nativeRequests := append([]oauthRequest{}, requests...)
			requests = nil
			tokenCalls = 0
			mu.Unlock()
			compareAuthSource(t, home, map[string]any{"now": 1700000000000, "name": "refresh-source", "config": config, "initial": initial, "operations": []map[string]any{{"action": action, "value": preserve}}}, []any{map[string]any{"value": value, "storage": storage}})
			mu.Lock()
			sourceRequests := append([]oauthRequest{}, requests...)
			mu.Unlock()
			if len(sourceRequests) > 0 && !reflect.DeepEqual(nativeRequests, sourceRequests) {
				t.Fatalf("actual refresh/revocation HTTP sequence differs\nGo %#v\nSource %#v", nativeRequests, sourceRequests)
			}
			// Lease directories must be released on success and OAuth failure alike.
			release, e := provider.refreshLock(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			release()
		})
	}
}
