package mcpauth

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"testing"
)

// Both implementations use the same actual loopback provider. Only clock and
// entropy primitives are deterministic; discovery, registration, callback,
// token exchange, validation, persistence and SDK decisions execute unchanged.
func TestCompleteConsentFlowAndCallbackFailuresAgainstActualSource(t *testing.T) {
	for _, kind := range []string{"dcr", "cimd", "basic", "post", "manual", "resource", "step-up", "mismatch", "denied", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			s, home := authFixture(t)
			s.options.Random = bytes.NewReader(make([]byte, 4096))
			events := []any{}
			s.options.OnEvent = func(name string, fields protocol.JSONObject) {
				events = append(events, map[string]any{"name": name, "fields": fields})
			}
			var host *httptest.Server
			var mu sync.Mutex
			requests := []oauthRequest{}
			host = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				requests = append(requests, oauthRequest{r.Method, r.URL.RequestURI(), string(body), r.Header.Get("Authorization")})
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/.well-known/oauth-protected-resource/mcp":
					if kind == "resource" || kind == "step-up" {
						_ = json.NewEncoder(w).Encode(map[string]any{"resource": host.URL + "/mcp", "authorization_servers": []string{host.URL}, "scopes_supported": []string{"resource-read", "resource-write"}})
					} else {
						w.WriteHeader(404)
					}
				case "/.well-known/oauth-authorization-server":
					metadata := map[string]any{"issuer": host.URL, "authorization_endpoint": host.URL + "/authorize", "token_endpoint": host.URL + "/token", "registration_endpoint": host.URL + "/register", "response_types_supported": []string{"code"}, "code_challenge_methods_supported": []string{"S256"}, "scopes_supported": []string{"read", "offline_access"}}
					if kind == "cimd" {
						metadata["client_id_metadata_document_supported"] = true
					}
					if kind == "post" {
						metadata["token_endpoint_auth_methods_supported"] = []string{"client_secret_post"}
					}
					_ = json.NewEncoder(w).Encode(metadata)
				case "/register":
					var registration map[string]any
					_ = json.Unmarshal(body, &registration)
					registration["client_id"] = "registered-client"
					registration["ignored_extension"] = "strip-this"
					_ = json.NewEncoder(w).Encode(registration)
				case "/token":
					_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "issued-access", "token_type": "Bearer", "refresh_token": "issued-refresh", "expires_in": "600", "scope": "read offline_access", "extension": "strip"})
				default:
					w.WriteHeader(404)
				}
			}))
			defer host.Close()
			port := freeCallbackPort(t)
			oauth := map[string]any{"callbackPort": port}
			if kind == "basic" || kind == "post" {
				oauth["clientId"] = "fixed-client"
			}
			config := record(field("type", "http"), field("url", host.URL+"/mcp"), field("oauth", oauth))
			initial := record(field("future", map[string]any{"keep": true}))
			if kind == "step-up" {
				key, _ := GetServerKey("source-flow", config)
				initial.Set("mcpOAuth", raw(map[string]any{key: map[string]any{"accessToken": "old-access", "expiresAt": 1700001000000, "stepUpScope": "admin write", "discoveryState": map[string]any{"resourceMetadataUrl": host.URL + "/.well-known/oauth-protected-resource/mcp"}}}))
			}
			if kind == "basic" || kind == "post" {
				key, _ := GetServerKey("source-flow", config)
				initial.Set("mcpOAuthClientConfig", raw(map[string]any{key: map[string]any{"clientSecret": "fixed-secret"}}))
			}
			if result := s.options.Secure.Update(context.Background(), initial); !result.Success {
				t.Fatal(result)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var address string
			var callback any
			delivered := make(chan struct{})
			var submit func(string)
			err := s.AuthenticateWithOptions(ctx, "source-flow", config, func(value string) error {
				defer close(delivered)
				address = value
				if kind == "cancel" {
					cancel()
					return nil
				}
				parsed, _ := url.Parse(value)
				state := parsed.Query().Get("state")
				if kind == "mismatch" {
					state = "wrong"
				}
				suffix := "?code=issued-code&state=" + url.QueryEscape(state)
				if kind == "denied" {
					suffix = "?error=access_denied&error_description=declined"
				}
				if kind == "manual" {
					submit("not a URL")
					submit(parsed.Query().Get("redirect_uri") + suffix)
					return nil
				}
				response, e := host.Client().Get(parsed.Query().Get("redirect_uri") + suffix)
				if e != nil {
					return e
				}
				defer response.Body.Close()
				body, e := io.ReadAll(response.Body)
				callback = map[string]any{"status": response.StatusCode, "body": string(body)}
				return e
			}, FlowOptions{SkipBrowserOpen: true, OnWaitingForCallback: func(handler func(string)) { submit = handler }})
			<-delivered
			var errValue any
			if err != nil {
				errValue = err.Error()
			}
			storage, e := s.read(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			observed := map[string]any{"url": address, "callback": callback, "error": errValue, "storage": storage, "events": events}
			mu.Lock()
			nativeRequests := append([]oauthRequest{}, requests...)
			requests = nil
			mu.Unlock()
			compareAuthSource(t, home, map[string]any{"mode": "flow", "captureEvents": true, "kind": kind, "name": "source-flow", "config": config, "now": 1700000000000, "initial": initial}, observed)
			mu.Lock()
			sourceRequests := append([]oauthRequest{}, requests...)
			mu.Unlock()
			if len(sourceRequests) > 0 && !reflect.DeepEqual(nativeRequests, sourceRequests) {
				t.Fatalf("actual OAuth HTTP requests differ\nGo %#v\nSource %#v", nativeRequests, sourceRequests)
			}
		})
	}
}
