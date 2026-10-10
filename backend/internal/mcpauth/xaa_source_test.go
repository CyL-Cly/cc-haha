package mcpauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
)

func TestCrossAppAccessActualTLSProviderAgainstSource(t *testing.T) {
	for _, kind := range []string{"basic", "post", "fallback", "resource-mismatch", "issuer-mismatch", "non-https", "grant-rejected", "idp-4xx", "idp-5xx", "idp-html", "idp-missing", "idp-wrong-type", "idp-shape", "as-html", "as-missing", "as-4xx"} {
		t.Run(kind, func(t *testing.T) {
			s, home := authFixture(t)
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
				case "/.well-known/oauth-protected-resource/mcp":
					resource := server.URL + "/mcp"
					if kind == "resource-mismatch" {
						resource = server.URL + "/other"
					}
					servers := []string{server.URL + "/as"}
					if kind == "fallback" {
						servers = []string{server.URL + "/skip", server.URL + "/as"}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"resource": resource, "authorization_servers": servers})
				case "/.well-known/oauth-authorization-server/as", "/.well-known/oauth-authorization-server/skip":
					issuer := server.URL + "/as"
					if r.URL.Path == "/.well-known/oauth-authorization-server/skip" {
						issuer = server.URL + "/skip"
					}
					if kind == "issuer-mismatch" {
						issuer = server.URL + "/wrong"
					}
					endpoint := server.URL + "/as-token"
					if kind == "non-https" {
						endpoint = "http://127.0.0.1/token"
					}
					metadata := map[string]any{"issuer": issuer, "authorization_endpoint": server.URL + "/auth", "token_endpoint": endpoint, "response_types_supported": []string{"code"}, "grant_types_supported": []string{jwtBearerGrant}}
					if kind == "grant-rejected" || r.URL.Path == "/.well-known/oauth-authorization-server/skip" {
						metadata["grant_types_supported"] = []string{"authorization_code"}
					}
					if kind == "post" {
						metadata["token_endpoint_auth_methods_supported"] = []string{"client_secret_post"}
					}
					_ = json.NewEncoder(w).Encode(metadata)
				case "/idp-token":
					switch kind {
					case "idp-4xx":
						w.WriteHeader(400)
						_, _ = w.Write([]byte(`{"error":"invalid_grant","subject_token":"secret"}`))
					case "idp-5xx":
						w.WriteHeader(500)
						_, _ = w.Write([]byte(`{"error":"server_error","client_secret":"secret"}`))
					case "idp-html":
						_, _ = w.Write([]byte("<html>proxy</html>"))
					case "idp-missing":
						_ = json.NewEncoder(w).Encode(map[string]any{"issued_token_type": idJAGType, "unknown": "strip-this"})
					case "idp-wrong-type":
						_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "jag-secret", "issued_token_type": "unexpected"})
					case "idp-shape":
						_ = json.NewEncoder(w).Encode(map[string]any{"access_token": 42, "issued_token_type": idJAGType})
					default:
						_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "jag-secret", "issued_token_type": idJAGType, "expires_in": "300", "unknown": true})
					}
				case "/as-token":
					switch kind {
					case "as-html":
						_, _ = w.Write([]byte("<html>proxy</html>"))
					case "as-missing":
						_ = json.NewEncoder(w).Encode(map[string]any{"scope": "read"})
					case "as-4xx":
						w.WriteHeader(400)
						_, _ = w.Write([]byte(`{"error":"invalid_grant","assertion":"jag-secret"}`))
					default:
						_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "xaa-access", "expires_in": "600", "refresh_token": "xaa-refresh", "id_token": "strip", "unknown": true})
					}
				default:
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			s.options.HTTPClient = server.Client()
			config := XaaConfig{"client:/ü", "secret:/ü", "idp-client", "idp-secret", "idp-token", server.URL + "/idp-token"}
			result, err := s.CrossAppAccess(context.Background(), server.URL+"/mcp", config)
			var value, errorValue, clear any
			if err == nil {
				value = result
			} else {
				errorValue = err.Error()
				var exchange *XaaTokenExchangeError
				if errors.As(err, &exchange) {
					clear = exchange.ShouldClearIDToken
				}
			}
			actual := map[string]any{"value": value, "error": errorValue, "clear": clear}
			mu.Lock()
			nativeRequests := append([]oauthRequest{}, requests...)
			requests = nil
			mu.Unlock()
			compareAuthSource(t, home, map[string]any{"mode": "xaa", "now": 1700000000000, "initial": map[string]any{}, "address": server.URL + "/mcp", "config": map[string]any{"clientId": config.ClientID, "clientSecret": config.ClientSecret, "idpClientId": config.IDPClientID, "idpClientSecret": config.IDPClientSecret, "idpIdToken": config.IDPIDToken, "idpTokenEndpoint": config.IDPTokenEndpoint}}, actual)
			mu.Lock()
			sourceRequests := append([]oauthRequest{}, requests...)
			mu.Unlock()
			if len(sourceRequests) > 0 && !reflect.DeepEqual(nativeRequests, sourceRequests) {
				t.Fatalf("actual XAA HTTP sequence differs\nGo %#v\nSource %#v", nativeRequests, sourceRequests)
			}
		})
	}
}
