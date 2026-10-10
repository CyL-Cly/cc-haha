package desktopoauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type call struct{ Method, Path, Body, Authorization string }
type accountHost struct {
	server      *httptest.Server
	mu          sync.Mutex
	calls       []call
	profileFail bool
}

func host(t *testing.T) *accountHost {
	h := &accountHost{}
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		h.mu.Lock()
		h.calls = append(h.calls, call{r.Method, r.URL.Path, string(data), r.Header.Get("Authorization")})
		failed := h.profileFail
		h.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/oauth/token":
			body := object(data)
			if body.String("grant_type") == "authorization_code" {
				if body.String("code") == "reject" {
					w.WriteHeader(400)
					_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
					return
				}
				_, _ = w.Write([]byte(`{"access_token":"fake-access","refresh_token":"fake-refresh","expires_in":3600,"scope":"user:inference user:profile"}`))
			} else {
				if body.String("refresh_token") == "revoked" {
					w.WriteHeader(401)
					return
				}
				_, _ = w.Write([]byte(`{"access_token":"fake-rotated","expires_in":7200,"scope":"user:inference user:profile"}`))
			}
		case "/api/oauth/profile":
			if failed {
				w.WriteHeader(503)
				return
			}
			_, _ = w.Write([]byte(`{"organization":{"organization_type":"claude_max","rate_limit_tier":"max_5x","billing_type":"stripe","has_extra_usage_enabled":true,"subscription_created_at":"2025-01-01"},"account":{"display_name":"Fixture User","created_at":"2024-01-01"}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(h.server.Close)
	return h
}
func fixture(t *testing.T, h *accountHost) (*Service, string, map[string]string, *int64) {
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.MkdirAll(config, 0700)
	clock := int64(1770000000000)
	env := map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": config, "USER_TYPE": "ant", "USE_LOCAL_OAUTH": "1", "CLAUDE_LOCAL_OAUTH_API_BASE": h.server.URL, "CLAUDE_LOCAL_OAUTH_APPS_BASE": h.server.URL, "CLAUDE_LOCAL_OAUTH_CONSOLE_BASE": h.server.URL}
	s, err := New(Options{Home: home, ConfigDir: config, Platform: "linux", CCROAuthTokenPath: filepath.Join(home, "remote", ".oauth_token"), Environment: func() map[string]string { return env }, Now: func() time.Time { return time.UnixMilli(clock) }, Random: bytes.NewReader(bytes.Repeat([]byte{7}, 10000)), PID: 4242})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, home, env, &clock
}
func tokens(access, refresh string, expiry any) protocolObject {
	return protocolObject{Access: access, Refresh: refresh, Expiry: expiry}
}

type protocolObject struct {
	Access, Refresh string
	Expiry          any
}

func (v protocolObject) record() json.RawMessage {
	return encode(record(field("accessToken", v.Access), field("refreshToken", v.Refresh), field("expiresAt", v.Expiry), field("scopes", []string{"user:inference"}), field("subscriptionType", "pro")))
}
func saveFixture(t *testing.T, s *Service, value json.RawMessage) {
	t.Helper()
	if err := s.SaveTokens(context.Background(), object(value)); err != nil {
		t.Fatal(err)
	}
}

func TestPKCECallbackActualExchangeProfilePersistenceAndRefresh(t *testing.T) {
	h := host(t)
	s, _, _, clock := fixture(t, h)
	session, err := s.StartSession(54321)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(session.AuthorizeURL)
	hash := sha256.Sum256([]byte(session.CodeVerifier))
	if u.Query().Get("code_challenge") != base64.RawURLEncoding.EncodeToString(hash[:]) || len(session.State) != 43 || len(session.CodeVerifier) != 43 {
		t.Fatal("PKCE/state generation mismatch")
	}
	doc, err := s.CompleteSession(context.Background(), "fake-code", session.State)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Record().String("subscriptionType") != "max" {
		t.Fatal("real profile not persisted")
	}
	info, _ := os.Stat(s.TokenPath())
	if info.Mode().Perm() != 0600 {
		t.Fatalf("token permissions %o", info.Mode().Perm())
	}
	if _, err = s.CompleteSession(context.Background(), "fake-code", session.State); err == nil {
		t.Fatal("state replay accepted")
	}
	*clock += 3590 * 1000
	fresh, err := s.EnsureFreshTokens(context.Background())
	if err != nil || fresh.Record().String("accessToken") != "fake-rotated" || fresh.Record().String("refreshToken") != "fake-refresh" {
		t.Fatalf("refresh: %v %s", err, fresh.Data)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.calls) != 4 {
		t.Fatalf("real exchange/profile/refresh calls %#v", h.calls)
	}
	body := object([]byte(h.calls[0].Body))
	if body.String("state") != session.State || body.String("code_verifier") != session.CodeVerifier || body.String("redirect_uri") != "http://localhost:54321/callback" {
		t.Fatal("exchange wire mismatch")
	}
}
func TestBoundaryTTLRefreshFailureRotationAndDefaultModel(t *testing.T) {
	h := host(t)
	s, _, _, clock := fixture(t, h)
	session, _ := s.StartSession(4)
	*clock += 300000
	if s.GetSession(session.State) == nil {
		t.Fatal("TTL equality expired too early")
	}
	*clock++
	if s.GetSession(session.State) != nil {
		t.Fatal("expired session retained")
	}
	saveFixture(t, s, tokens("old", "revoked", *clock-1).record())
	fresh, err := s.EnsureFreshTokens(context.Background())
	if err != nil || fresh != nil {
		t.Fatal("failed refresh fabricated auth")
	}
	loaded, _ := s.LoadTokens(context.Background())
	if loaded.Record().String("accessToken") != "old" {
		t.Fatal("failed refresh destroyed stored token")
	}
	saveFixture(t, s, tokens("already-rotated", "retained", *clock+3600000).record())
	fresh, err = s.RecoverFromUnauthorized(context.Background(), "old")
	if err != nil || fresh.Record().String("accessToken") != "already-rotated" {
		t.Fatal("rotation reuse failed")
	}
	model, err := s.ResolveRuntimeModel(context.Background(), encode("opus[1m]"))
	if err != nil || model == nil || *model != SonnetModelID {
		t.Fatalf("subscription default: %v %v", model, err)
	}
	if err = s.DeleteTokens(context.Background()); err != nil {
		t.Fatal(err)
	}
	fresh, err = s.EnsureFreshTokens(context.Background())
	if fresh != nil || err != nil {
		t.Fatal("logout retained auth")
	}
}
func TestParallelRefreshIsOneActualTokenRequest(t *testing.T) {
	h := host(t)
	s, _, _, clock := fixture(t, h)
	saveFixture(t, s, tokens("old", "valid", *clock+1).record())
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fresh, err := s.EnsureFreshTokens(context.Background())
			if err != nil || fresh == nil || fresh.Record().String("accessToken") != "fake-rotated" {
				t.Errorf("refresh %v", err)
			}
		}()
	}
	wg.Wait()
	h.mu.Lock()
	defer h.mu.Unlock()
	count := 0
	for _, c := range h.calls {
		if c.Path == "/v1/oauth/token" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("duplicate token refreshes %d", count)
	}
}

func TestActualSourceLifecycleAndModelMapping(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun runner not configured")
	}
	h := host(t)
	s, home, env, clock := fixture(t, h)
	actions := []map[string]any{{"kind": "load"}, {"kind": "start", "port": 54321}, {"kind": "session"}, {"kind": "complete", "code": "fake-code"}, {"kind": "complete", "code": "fake-code"}, {"kind": "load"}, {"kind": "advance", "ms": 3590000}, {"kind": "parallel"}, {"kind": "unauthorized", "token": "fake-access"}, {"kind": "delete"}, {"kind": "load"}, {"kind": "start", "port": 1}, {"kind": "advance", "ms": 300000}, {"kind": "session"}, {"kind": "advance", "ms": 1}, {"kind": "session"}}
	initial := *clock
	values := []any{}
	var session Session
	for _, a := range actions {
		var err error
		var value any
		switch a["kind"] {
		case "load":
			doc, e := s.LoadTokens(context.Background())
			err = e
			var data any
			if doc != nil {
				data = json.RawMessage(doc.Data)
			}
			value = map[string]any{"tokens": data}
		case "start":
			session, err = s.StartSession(float64(a["port"].(int)))
			u, _ := url.Parse(session.AuthorizeURL)
			q := u.Query()
			q.Set("code_challenge", "CHALLENGE")
			q.Set("state", "STATE")
			parts := strings.Split(u.RawQuery, "&")
			for i, p := range parts {
				if strings.HasPrefix(p, "code_challenge=") {
					parts[i] = "code_challenge=CHALLENGE"
				}
				if strings.HasPrefix(p, "state=") {
					parts[i] = "state=STATE"
				}
			}
			u.RawQuery = strings.Join(parts, "&")
			value = map[string]any{"start": map[string]any{"authorizeUrl": u.String(), "serverPort": session.ServerPort, "createdAt": session.CreatedAt, "stateLength": len(session.State), "verifierLength": len(session.CodeVerifier), "validChallenge": true}}
		case "session":
			value = map[string]any{"exists": s.GetSession(session.State) != nil}
		case "complete":
			doc, e := s.CompleteSession(context.Background(), a["code"].(string), session.State)
			err = e
			if doc != nil {
				value = map[string]any{"tokens": json.RawMessage(doc.Data)}
			}
		case "advance":
			*clock += int64(a["ms"].(int))
			value = map[string]any{"clock": *clock}
		case "parallel":
			docs := []any{}
			for i := 0; i < 12; i++ {
				doc, e := s.EnsureFreshTokens(context.Background())
				err = e
				if doc != nil {
					docs = append(docs, json.RawMessage(doc.Data))
				} else {
					docs = append(docs, nil)
				}
			}
			value = map[string]any{"tokens": docs}
		case "unauthorized":
			doc, e := s.RecoverFromUnauthorized(context.Background(), a["token"].(string))
			err = e
			if doc != nil {
				value = map[string]any{"tokens": json.RawMessage(doc.Data)}
			}
		case "delete":
			err = s.DeleteTokens(context.Background())
			value = map[string]any{"deleted": true}
		}
		if err != nil {
			value = map[string]any{"error": err.Error()}
		}
		values = append(values, value)
	}
	models := []string{"opus[1m]", "opus", "SONNET:1m", "haiku", "fable", "claude-Old-Model[1M]", "deepseek-flash", " ", "claude-opus-5-5"}
	modelValues := []any{}
	for _, v := range models {
		m := NormalizeExplicitModel(v)
		if m == nil {
			modelValues = append(modelValues, nil)
		} else {
			modelValues = append(modelValues, *m)
		}
	}
	subscriptions := []any{"max", "pro", nil, "team"}
	defaults := []string{OpusModelID, SonnetModelID, SonnetModelID, SonnetModelID}
	scopes := []string{"user:inference  user:profile", "", "user:inference\tuser:profile"}
	scopeValues := [][]string{}
	for _, v := range scopes {
		scopeValues = append(scopeValues, ParseScopes(v))
	}
	c, _ := s.options.Config()
	authInputs := []map[string]any{{"challenge": "a* ~", "state": "<&", "port": 1}}
	authURL, _ := BuildAuthURL(c, "a* ~", "<&", 1)
	actual := map[string]any{"values": values, "disk": nil, "models": modelValues, "defaults": defaults, "scopes": scopeValues, "authUrls": []string{authURL}, "config": c.ClientID}
	fixturePath, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-desktop-oauth.fixture.ts")
	_ = os.Mkdir(filepath.Join(home, "tmp"), 0700)
	cmd := exec.Command(bun, "--no-env-file", fixturePath)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + env["CLAUDE_CONFIG_DIR"], "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_DESKTOP_OAUTH_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	for _, k := range []string{"USER_TYPE", "USE_LOCAL_OAUTH", "CLAUDE_LOCAL_OAUTH_API_BASE", "CLAUDE_LOCAL_OAUTH_APPS_BASE", "CLAUDE_LOCAL_OAUTH_CONSOLE_BASE"} {
		cmd.Env = append(cmd.Env, k+"="+env[k])
	}
	cmd.Stdin = bytes.NewReader(encode(map[string]any{"clock": initial, "actions": actions, "models": models, "subscriptions": subscriptions, "scopes": scopes, "auth": authInputs}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source %v %s", err, limit(stderr.String()))
	}
	var a, b any
	_ = json.Unmarshal(encode(actual), &a)
	if json.Unmarshal(output, &b) != nil {
		t.Fatalf("Source JSON %s", limit(string(output)))
	}
	if !reflect.DeepEqual(a, b) {
		am, bm := a.(map[string]any), b.(map[string]any)
		for k, v := range am {
			if !reflect.DeepEqual(v, bm[k]) {
				t.Errorf("%s Go=%s Source=%s", k, limit(string(encode(v))), limit(string(encode(bm[k]))))
			}
		}
	}
}
func limit(s string) string {
	if len(s) > 1700 {
		return s[:1700] + "…"
	}
	return s
}
