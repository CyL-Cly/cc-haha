package mcpauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func IssuerKey(value string) string {
	if u, err := parseURL(value); err == nil {
		u.SetPathname(strings.TrimRight(u.Pathname(), "/"))
		u.SetHost(strings.ToLower(u.Host()))
		return u.Href(false)
	}
	return strings.TrimRight(value, "/")
}
func sliceUTF16(value string, size int) string {
	units := utf16.Encode([]rune(value))
	if len(units) > size {
		units = units[:size]
	}
	return string(utf16.Decode(units))
}
func (s *Service) CachedIDPIDToken(ctx context.Context, issuer string) (string, error) {
	data, err := s.read(ctx)
	if err != nil {
		return "", err
	}
	entry := object(object(data.Get("mcpXaaIdp")).Get(IssuerKey(issuer)))
	if len(entry.OwnProperties()) == 0 || number(entry.Get("expiresAt"))-float64(s.options.Now().UnixMilli()) <= 60000 {
		return "", nil
	}
	return entry.String("idToken"), nil
}
func (s *Service) saveIDPIDToken(ctx context.Context, issuer, token string, expires float64) error {
	return s.change(ctx, func(data protocol.JSONObject) error {
		slots := object(data.Get("mcpXaaIdp"))
		slots.Set(IssuerKey(issuer), raw(record(field("idToken", token), protocol.JSONField{Name: "expiresAt", Value: jsonNumber(expires)})))
		data.Set("mcpXaaIdp", raw(slots))
		return nil
	})
}

// Buffer.from(base64url) in Source ignores non-alphabet bytes and accepts
// either base64 alphabet. JWT expiry is only a cache TTL hint, as in Source.
func cleanJWTBase64(value string) string {
	var out strings.Builder
	for _, r := range value {
		if r == '=' {
			break
		}
		if r == '+' {
			r = '-'
		}
		if r == '/' {
			r = '_'
		}
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			out.WriteRune(r)
		}
	}
	result := out.String()
	if len(result)%4 == 1 {
		result = result[:len(result)-1]
	}
	return result
}
func jwtExpiry(token string) float64 {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return 0
	}
	payload, err := base64.RawURLEncoding.DecodeString(cleanJWTBase64(parts[1]))
	if err != nil {
		return 0
	}
	value := object(protocol.DecodeJavaScriptUTF8(payload))
	if v := value.Get("exp"); v != nil && len(v) > 0 && v[0] != '"' && string(v) != "null" && string(v) != "true" && string(v) != "false" {
		return number(v)
	}
	return 0
}
func (s *Service) SaveIDPIDTokenFromJWT(ctx context.Context, issuer, token string) (float64, error) {
	expires := jwtExpiry(token) * 1000
	if expires == 0 {
		expires = float64(s.options.Now().UnixMilli()) + 3600*1000
	}
	return expires, s.saveIDPIDToken(ctx, issuer, token, expires)
}
func (s *Service) ClearIDPIDToken(ctx context.Context, issuer string) error {
	return s.clearIssuerSlot(ctx, "mcpXaaIdp", issuer)
}
func (s *Service) clearIssuerSlot(ctx context.Context, slot, issuer string) error {
	data, err := s.read(ctx)
	if err != nil {
		return err
	}
	key := IssuerKey(issuer)
	if !truthy(object(data.Get(slot)).Get(key)) {
		return nil
	}
	return s.change(ctx, func(data protocol.JSONObject) error {
		slots := object(data.Get(slot))
		slots.Delete(key)
		data.Set(slot, raw(slots))
		return nil
	})
}
func (s *Service) SaveIDPClientSecret(ctx context.Context, issuer, secret string) (securestorage.Result, error) {
	return s.options.Secure.Change(ctx, func(data protocol.JSONObject) error {
		slots := object(data.Get("mcpXaaIdpConfig"))
		slots.Set(IssuerKey(issuer), raw(record(field("clientSecret", secret))))
		data.Set("mcpXaaIdpConfig", raw(slots))
		return nil
	})
}
func (s *Service) IDPClientSecret(ctx context.Context, issuer string) (string, error) {
	data, err := s.read(ctx)
	if err != nil {
		return "", err
	}
	return object(object(data.Get("mcpXaaIdpConfig")).Get(IssuerKey(issuer))).String("clientSecret"), nil
}
func (s *Service) ClearIDPClientSecret(ctx context.Context, issuer string) error {
	return s.clearIssuerSlot(ctx, "mcpXaaIdpConfig", issuer)
}
func (s *Service) DiscoverOIDC(ctx context.Context, issuer string) (protocol.JSONObject, error) {
	base := issuer
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	u, err := parseURL(base)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	u.SetPathname(u.Pathname() + ".well-known/openid-configuration")
	address := u.Href(false)
	response, data, err := s.request(ctx, "GET", address, http.Header{"Accept": {"application/json"}}, nil, false)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return protocol.JSONObject{}, fmt.Errorf("XAA IdP: OIDC discovery failed: HTTP %d at %s", response.StatusCode, address)
	}
	if !json.Valid(data) {
		return protocol.JSONObject{}, fmt.Errorf("XAA IdP: OIDC discovery returned non-JSON at %s (captive portal or proxy?)", address)
	}
	metadata, err := validateMetadata(object(data), "oidc")
	if err != nil {
		return protocol.JSONObject{}, fmt.Errorf("XAA IdP: invalid OIDC metadata: %s", err.Error())
	}
	token, err := parseURL(metadata.String("token_endpoint"))
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if token.Scheme() != "https" {
		return protocol.JSONObject{}, fmt.Errorf("XAA IdP: refusing non-HTTPS token endpoint: %s", metadata.String("token_endpoint"))
	}
	return metadata, nil
}
func (s *Service) AcquireIDPIDToken(ctx context.Context, issuer, clientID, secret string, callbackPort json.RawMessage, onURL func(string) error, skipBrowser bool) (string, error) {
	if cached, err := s.CachedIDPIDToken(ctx, issuer); err != nil || cached != "" {
		return cached, err
	}
	metadata, err := s.DiscoverOIDC(ctx, issuer)
	if err != nil {
		return "", err
	}
	config := record(field("type", "http"), field("url", issuer))
	oauth := protocol.NewJSONObject()
	if callbackPort != nil {
		oauth.Set("callbackPort", callbackPort)
	}
	config.Set("oauth", raw(oauth))
	port, err := s.callbackPort(config)
	if err != nil {
		return "", err
	}
	provider, err := s.Provider(ctx, "xaa", config)
	if err != nil {
		return "", err
	}
	provider.redirect = fmt.Sprintf("http://localhost:%d/callback", port)
	state, err := provider.State()
	if err != nil {
		return "", err
	}
	if !containsString(metadata.Get("response_types_supported"), "code") {
		return "", errors.New("Incompatible auth server: does not support response type code")
	}
	if methods := metadata.Get("code_challenge_methods_supported"); methods != nil && !containsString(methods, "S256") {
		return "", errors.New("Incompatible auth server: does not support code challenge method S256")
	}
	verifier, challenge, err := provider.pkce()
	if err != nil {
		return "", err
	}
	authorization, err := parseURL(metadata.String("authorization_endpoint"))
	if err != nil {
		return "", err
	}
	query := authorization.SearchParams()
	query.Set("response_type", "code")
	query.Set("client_id", clientID)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	query.Set("redirect_uri", provider.redirect)
	query.Set("state", state)
	query.Set("scope", "openid")
	encodeSearchParams(authorization)
	if ctx.Err() != nil {
		return "", errors.New("XAA IdP: login cancelled")
	}
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		message := s.callbackListenError(port, err).Error()
		if strings.Contains(message, "already in use") {
			command := fmt.Sprintf("lsof -ti:%d -sTCP:LISTEN", port)
			if s.options.Platform == "windows" || s.options.Platform == "win32" {
				command = fmt.Sprintf("netstat -ano | findstr :%d", port)
			}
			return "", fmt.Errorf("XAA IdP: callback port %d is already in use. Run `%s` to find the holder.", port, command)
		}
		return "", fmt.Errorf("XAA IdP: callback server failed: %s", err.Error())
	}
	flow, cancel := context.WithTimeout(ctx, FlowTimeout)
	defer cancel()
	completed := make(chan callbackReply, 1)
	var once sync.Once
	complete := func(code string, err error) { once.Do(func() { completed <- callbackReply{code, err} }) }
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			w.WriteHeader(404)
			return
		}
		query := r.URL.Query()
		failure := callbackValues(query, "error")
		description := callbackValues(query, "error_description")
		w.Header().Set("Content-Type", "text/html")
		if failure != "" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte("<html><body><h3>IdP login failed</h3><p>" + sanitizeCallbackHTML(failure) + "</p><p>" + sanitizeCallbackHTML(description) + "</p></body></html>"))
			message := "XAA IdP: " + failure
			if description != "" {
				message += " — " + description
			}
			complete("", errors.New(message))
			return
		}
		if states := query["state"]; len(states) != 1 || states[0] != state {
			w.WriteHeader(400)
			_, _ = w.Write([]byte("<html><body><h3>State mismatch</h3></body></html>"))
			complete("", errors.New("XAA IdP: state mismatch (possible CSRF)"))
			return
		}
		code := callbackValues(query, "code")
		if code == "" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte("<html><body><h3>Missing code</h3></body></html>"))
			complete("", errors.New("XAA IdP: callback missing code"))
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte("<html><body><h3>IdP login complete — you can close this window.</h3></body></html>"))
		complete(code, nil)
	})}
	defer func() { cancel(); _ = server.Close(); _ = listener.Close() }()
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			complete("", fmt.Errorf("XAA IdP: callback server failed: %s", err.Error()))
		}
	}()
	if onURL != nil {
		if err = onURL(authorization.Href(false)); err != nil {
			return "", err
		}
	}
	if !skipBrowser {
		go func() { _, _ = s.options.OpenBrowser(ctx, authorization.Href(false)) }()
	}
	var code string
	select {
	case reply := <-completed:
		_ = listener.Close()
		if reply.err != nil {
			return "", reply.err
		}
		code = reply.code
	case <-flow.Done():
		if ctx.Err() != nil {
			return "", errors.New("XAA IdP: login cancelled")
		}
		return "", errors.New("XAA IdP: login timed out")
	}
	client := record(field("client_id", clientID))
	if secret != "" {
		client.Set("client_secret", quote(secret))
	}
	tokens, err := s.tokenRequest(ctx, issuer, &metadata, &client, formPairs{{"grant_type", "authorization_code"}, {"code", code}, {"code_verifier", verifier}, {"redirect_uri", provider.redirect}}, "", false)
	if err != nil {
		return "", err
	}
	idToken := tokens.String("id_token")
	if idToken == "" {
		return "", errors.New("XAA IdP: token response missing id_token (check scope=openid)")
	}
	expires := jwtExpiry(idToken) * 1000
	if expires == 0 {
		ttl := number(tokens.Get("expires_in"))
		if tokens.Get("expires_in") == nil || string(tokens.Get("expires_in")) == "null" {
			ttl = 3600
		}
		expires = float64(s.options.Now().UnixMilli()) + ttl*1000
	}
	return idToken, s.saveIDPIDToken(ctx, issuer, idToken, expires)
}
