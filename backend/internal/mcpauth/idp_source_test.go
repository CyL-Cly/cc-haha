package mcpauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"testing"
)

func TestIdPActualOIDCLoginAndCallbackAgainstSource(t *testing.T) {
	for _, kind := range []string{"success", "mismatch", "denied", "cancel", "missing-token"} {
		t.Run(kind, func(t *testing.T) {
			s, home := authFixture(t)
			s.options.Random = bytes.NewReader(make([]byte, 4096))
			initial := record(field("future", map[string]any{"keep": true}))
			if result := s.options.Secure.Update(context.Background(), initial); !result.Success {
				t.Fatal(result)
			}
			token := "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"exp":1700001000}`)) + ".signature"
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
				case "/tenant/.well-known/openid-configuration":
					_ = json.NewEncoder(w).Encode(map[string]any{"issuer": server.URL + "/tenant", "authorization_endpoint": server.URL + "/authorize", "token_endpoint": server.URL + "/token", "jwks_uri": server.URL + "/jwks", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}, "code_challenge_methods_supported": []string{"S256"}})
				case "/token":
					tokens := map[string]any{"access_token": "idp-access", "token_type": "Bearer", "expires_in": 600}
					if kind != "missing-token" {
						tokens["id_token"] = token
					}
					_ = json.NewEncoder(w).Encode(tokens)
				default:
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			s.options.HTTPClient = server.Client()
			port := freeCallbackPort(t)
			config := record(field("oauth", map[string]any{"callbackPort": port}))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var address string
			var callback any
			value, err := s.AcquireIDPIDToken(ctx, server.URL+"/tenant", "idp-client", "idp-secret", raw(port), func(u string) error {
				address = u
				if kind == "cancel" {
					cancel()
					return nil
				}
				parsed, _ := url.Parse(u)
				state := parsed.Query().Get("state")
				if kind == "mismatch" {
					state = "wrong"
				}
				suffix := "?code=issued-code&state=" + url.QueryEscape(state)
				if kind == "denied" {
					suffix = "?error=access_denied&error_description=declined"
				}
				response, e := server.Client().Get(parsed.Query().Get("redirect_uri") + suffix)
				if e != nil {
					return e
				}
				defer response.Body.Close()
				body, e := io.ReadAll(response.Body)
				callback = map[string]any{"status": response.StatusCode, "body": string(body)}
				return e
			}, true)
			var errorValue, valueResult any
			if err != nil {
				errorValue = err.Error()
			} else {
				valueResult = value
			}
			storage, e := s.read(ctx)
			if ctx.Err() != nil {
				storage, e = s.read(context.Background())
			}
			if e != nil {
				t.Fatal(e)
			}
			mu.Lock()
			nativeRequests := append([]oauthRequest{}, requests...)
			requests = nil
			mu.Unlock()
			compareAuthSource(t, home, map[string]any{"mode": "idplogin", "kind": kind, "config": config, "address": server.URL + "/tenant", "now": 1700000000000, "initial": initial}, map[string]any{"url": address, "callback": callback, "error": errorValue, "value": valueResult, "storage": storage})
			mu.Lock()
			sourceRequests := append([]oauthRequest{}, requests...)
			mu.Unlock()
			if len(sourceRequests) > 0 && !reflect.DeepEqual(nativeRequests, sourceRequests) {
				t.Fatalf("actual IdP HTTP requests differ\nGo %#v\nSource %#v", nativeRequests, sourceRequests)
			}
		})
	}
}
