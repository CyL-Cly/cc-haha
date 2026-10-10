package cliauth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
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

func TestActualSourceCLIRefreshLoginPhysicalInstallation(t *testing.T) {
	actualSourceCLILogin(t, true, false, false)
}
func TestActualSourceCLIBrowserLoginPhysicalInstallation(t *testing.T) {
	actualSourceCLILogin(t, false, false, false)
}
func TestActualSourceCLIConsoleLoginPhysicalInstallation(t *testing.T) {
	actualSourceCLILogin(t, false, true, false)
}
func TestActualSourceCLIRefreshCachedSubscriptionRetained(t *testing.T) {
	actualSourceCLILogin(t, true, false, true)
}
func TestActualSourceCLIInstallationTokenAndDateBoundaries(t *testing.T) {
	for _, variant := range []string{"invalid-date", "missing-expiry", "no-refresh-browser", "token-account-fallback", "missing-console-key", "storage-failure", "console-security-nonzero", "console-security-missing"} {
		t.Run(variant, func(t *testing.T) {
			actualSourceCLILogin(t, false, strings.HasPrefix(variant, "console-security-") || variant == "missing-console-key", false, variant)
		})
	}
}
func TestActualSourceCLILoginPolicyAndRuntimeHints(t *testing.T) {
	for _, variant := range []string{"policy-match", "policy-mismatch", "policy-env-mismatch", "policy-unverified", "policy-thirdparty", "policy-expired", "user-org-only", "force-claudeai", "email-sso"} {
		t.Run(variant, func(t *testing.T) { actualSourceCLILogin(t, false, variant == "force-claudeai", false, variant) })
	}
}
func actualSourceCLILogin(t *testing.T, refreshMode, consoleMode, cached bool, variants ...string) {
	variant := ""
	if len(variants) > 0 {
		variant = variants[0]
	}
	inputConsole := consoleMode
	if variant == "force-claudeai" {
		consoleMode = false
	}
	policy := protocol.NewJSONObject()
	if strings.HasPrefix(variant, "policy-") {
		required := "fixture-org"
		if variant != "policy-match" {
			required = "managed-required-org"
		}
		policy.Set("forceLoginOrgUUID", json.RawMessage(`"`+required+`"`))
	}
	expectStored := !consoleMode && variant != "missing-expiry" && variant != "no-refresh-browser" && variant != "storage-failure"

	home := t.TempDir()
	if p, e := filepath.EvalSymlinks(home); e == nil {
		home = p
	}
	config := filepath.Join(home, ".claude")
	_ = os.Mkdir(config, 0700)
	userSettings := `{}`
	if variant == "user-org-only" {
		userSettings = `{"forceLoginOrgUUID":"user-hint-only"}`
	}
	if variant == "force-claudeai" {
		userSettings = `{"forceLoginMethod":"claudeai"}`
	}
	_ = os.WriteFile(filepath.Join(config, "settings.json"), []byte(userSettings), 0600)
	initialGlobal := []byte(`{"future":{"keep":true},"hasCompletedOnboarding":false,"primaryApiKey":"old-fixture","oauthAccount":{"old":true}}`)
	initialSecure := []byte(`{"claudeAiOauth":{"old":true},"openaiCodexOauth":{"old":true},"oldSecure":true}`)
	if cached {
		initialGlobal = []byte(`{"future":{"keep":true},"hasCompletedOnboarding":false,"oauthAccount":{"billingType":"stripe","accountCreatedAt":"2023-01-01","subscriptionCreatedAt":"2023-02-01"}}`)
		initialSecure = []byte(`{"claudeAiOauth":{"accessToken":"fixture-old","refreshToken":"fixture-old-refresh","expiresAt":9999999999999,"scopes":["user:profile","user:inference"],"subscriptionType":"pro","rateLimitTier":"cached-pro-tier"},"oldSecure":true}`)
	}
	_ = os.WriteFile(filepath.Join(config, ".claude.json"), initialGlobal, 0600)
	_ = os.WriteFile(filepath.Join(home, "secure-fixture.json"), initialSecure, 0600)
	if variant == "storage-failure" {
		path := filepath.Join(home, "secure-fixture.json")
		_ = os.Remove(path)
		_ = os.Mkdir(path, 0700)
		_ = os.WriteFile(filepath.Join(path, "owned-blocker"), []byte("owned"), 0600)
	}

	var mu sync.Mutex
	paths := []string{}
	profileSeen := 0
	leaseObservations := []bool{}
	requestHeaders := []map[string]string{}
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/api/oauth/profile" {
			profileSeen++
		}
		currentProfile := profileSeen
		headers := map[string]string{}
		for _, name := range []string{"Accept", "Content-Type", "User-Agent", "Authorization", "Anthropic-Beta", "Accept-Encoding"} {
			headers[name] = r.Header.Get(name)
		}
		requestHeaders = append(requestHeaders, headers)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/oauth/token":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			isRefresh := refreshMode || variant == "policy-expired" && body["grant_type"] == "refresh_token"
			if isRefresh && variant == "policy-expired" {
				_, leaseError := os.Stat(config + ".lock")
				mu.Lock()
				leaseObservations = append(leaseObservations, leaseError == nil)
				mu.Unlock()
			}
			expectedRefresh := "fixture-refresh"
			if !refreshMode {
				expectedRefresh = "fixture-new-refresh"
			}
			if isRefresh && (body["grant_type"] != "refresh_token" || body["refresh_token"] != expectedRefresh) || !isRefresh && (body["grant_type"] != "authorization_code" || body["code"] != "fixture-authorization-code" || body["code_verifier"] == "" || body["state"] == "") {
				t.Error("actual Source refresh request producer differs")
			}
			scope := "user:profile user:inference"
			if consoleMode {
				scope = "org:create_api_key user:profile"
			}
			reply := map[string]any{"access_token": "fixture-access", "refresh_token": "fixture-new-refresh", "expires_in": 3600, "scope": scope}
			if variant == "policy-expired" && !isRefresh {
				reply["expires_in"] = 1
			}
			if variant == "missing-expiry" {
				delete(reply, "expires_in")
			}
			if variant == "no-refresh-browser" {
				delete(reply, "refresh_token")
			}
			if variant == "token-account-fallback" {
				reply["account"] = map[string]any{"uuid": "fallback-account", "email_address": "fallback@example.invalid"}
				reply["organization"] = map[string]any{"uuid": "fallback-org"}
			}
			_ = json.NewEncoder(w).Encode(reply)
		case "/api/oauth/profile":
			if variant == "token-account-fallback" || variant == "policy-unverified" && currentProfile%2 == 0 {
				http.Error(w, "owned unavailable profile", 503)
				return
			}
			_, _ = w.Write([]byte(`{"account":{"uuid":"fixture-account","email":"fixture@example.invalid","display_name":"Fixture","created_at":"2024-01-01"},"organization":{"uuid":"fixture-org","organization_type":"claude_max","rate_limit_tier":"default_claude_max_5x","billing_type":"stripe","subscription_created_at":"2024-01-02","has_extra_usage_enabled":false}}`))
		case "/api/oauth/claude_cli/roles":
			_, _ = w.Write([]byte(`{"organization_role":"owner","workspace_role":"admin","organization_name":"Fixture Org"}`))
		case "/api/oauth/claude_cli/create_api_key":
			if variant == "missing-console-key" {
				_, _ = w.Write([]byte(`{}`))
				return
			}
			_, _ = w.Write([]byte(`{"raw_key":"fixture-console-key"}`))
		case "/api/organization/claude_code_first_token_date":
			if variant == "invalid-date" {
				_, _ = w.Write([]byte(`{"first_token_date":"not-a-date"}`))
			} else {
				_, _ = w.Write([]byte(`{"first_token_date":"2024-03-01T00:00:00Z"}`))
			}
		default:
			t.Error("unowned Source OAuth request", r.URL.Path)
			http.Error(w, "unowned", 404)
		}
	}))
	defer peer.Close()
	cert := filepath.Join(home, "cert.pem")
	_ = os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}), 0600)
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	fixture, _ := filepath.Abs("../../testdata/native-cli-auth-login.fixture.ts")
	profile, _ := filepath.Abs("../../testdata/cli-auth-loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "PATH=/usr/bin:/bin", "USER=fixture", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLI_LOGIN_FIXTURE=1", "CC_HAHA_CLI_LOGIN_PEER=" + peer.URL, "CLAUDE_CODE_OAUTH_REFRESH_TOKEN=fixture-refresh", "CLAUDE_CODE_OAUTH_SCOPES=user:profile user:inference", "NODE_EXTRA_CA_CERTS=" + cert, "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "USER_TYPE=external", "NODE_ENV=production"}
	if !refreshMode {
		for i, pair := range cmd.Env {
			if strings.HasPrefix(pair, "CLAUDE_CODE_OAUTH_REFRESH_TOKEN=") {
				cmd.Env = append(cmd.Env[:i], cmd.Env[i+1:]...)
				break
			}
		}
	}
	if strings.HasPrefix(variant, "console-security-") {
		cmd.Env = append(cmd.Env, "CC_HAHA_CLI_LOGIN_SECURITY_MODE="+strings.TrimPrefix(variant, "console-security-"))
	}
	if inputConsole {
		cmd.Env = append(cmd.Env, `CC_HAHA_CLI_LOGIN_OPTIONS={"console":true}`)
	}
	if len(policy.OwnProperties()) > 0 {
		policyRaw, _ := policy.MarshalJSON()
		cmd.Env = append(cmd.Env, "CC_HAHA_CLI_LOGIN_POLICY="+string(policyRaw))
	}
	if variant == "policy-env-mismatch" {
		cmd.Env = append(cmd.Env, "CLAUDE_CODE_OAUTH_TOKEN=fixture-env-token")
	}
	if variant == "policy-thirdparty" {
		cmd.Env = append(cmd.Env, "CLAUDE_CODE_USE_BEDROCK=1")
	}
	if variant == "email-sso" {
		cmd.Env = append(cmd.Env, `CC_HAHA_CLI_LOGIN_OPTIONS={"email":"fixture+ü@example.invalid","sso":true}`)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	sourceExit := 0
	if e != nil {
		var exit *exec.ExitError
		if errors.As(e, &exit) {
			sourceExit = exit.ExitCode()
		} else {
			t.Fatal("actual Source authLogin", e, stderr.String())
		}
	}
	if sourceExit != 0 && !strings.HasPrefix(variant, "policy-") && variant != "missing-console-key" {
		t.Fatal("actual Source authLogin", e, stderr.String())
	}
	if refreshMode && string(output) != "Login successful.\n" || !refreshMode && sourceExit == 0 && (!strings.HasPrefix(string(output), "Opening browser to sign in…\nIf the browser didn't open, visit: ") || !strings.HasSuffix(string(output), "Login successful.\n")) {
		t.Fatal("actual Source output", string(output))
	}
	secure, e := os.ReadFile(filepath.Join(home, "secure-fixture.json"))
	if expectStored && (e != nil || !strings.Contains(string(secure), "fixture-new-refresh") || strings.Contains(string(secure), "openaiCodexOauth")) || !expectStored && variant != "storage-failure" && !os.IsNotExist(e) {
		t.Fatal("actual Source secure install", e, string(secure))
	}
	global, e := os.ReadFile(filepath.Join(config, ".claude.json"))
	if e != nil {
		t.Fatal(e)
	}
	var fields map[string]any
	_ = json.Unmarshal(global, &fields)
	if fields["hasCompletedOnboarding"] != (refreshMode && sourceExit == 0) || fields["future"] == nil || variant != "console-security-missing" && fields["primaryApiKey"] != nil || fields["oauthAccount"] == nil {
		t.Fatal("actual Source global installation", string(global))
	}

	if variant == "storage-failure" {
		raw, e := os.ReadFile(filepath.Join(home, "secure-write-failure.json"))
		if e != nil || !strings.Contains(string(raw), "EISDIR") {
			t.Fatal("actual Source secure FS failure", e, string(raw))
		}
	}
	if variant == "policy-expired" {
		if _, e := os.Stat(config + ".lock"); !os.IsNotExist(e) {
			t.Fatal("Source did not release physical directory lease", e)
		}
	}
	sourceRequestCount := len(paths)
	sourceSecure := append([]byte(nil), secure...)
	sourceGlobal := fields
	_ = os.WriteFile(filepath.Join(config, ".claude.json"), initialGlobal, 0600)
	_ = os.WriteFile(filepath.Join(config, ".credentials.json"), initialSecure, 0600)
	if variant == "storage-failure" {
		path := filepath.Join(config, ".credentials.json")
		_ = os.Remove(path)
		_ = os.Mkdir(path, 0700)
		_ = os.WriteFile(filepath.Join(path, "owned-blocker"), []byte("owned"), 0600)
	}
	env := map[string]string{}
	for _, pair := range cmd.Env {
		key, value, ok := strings.Cut(pair, "=")
		if ok {
			env[key] = value
		}
	}
	n, e := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: home, Platform: "darwin", ManagedRoot: filepath.Join(home, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return &policy, nil, nil }})
	if e != nil {
		t.Fatal(e)
	}
	n.RunSecurity = func(_ context.Context, security securestorage.Command) ([]byte, error) {
		if consoleMode && len(security.Args) == 1 && security.Args[0] == "-i" {
			_ = os.WriteFile(filepath.Join(home, "go-legacy-save.txt"), []byte(security.Input), 0600)
			if variant == "console-security-nonzero" {
				return exec.CommandContext(ctx, "/usr/bin/false").Output()
			}
			if variant == "console-security-missing" {
				return exec.CommandContext(ctx, filepath.Join(home, "owned-missing-security")).Output()
			}
			return nil, nil
		}
		return nil, errors.New("owned external Keychain absent")
	}
	var goOut, goError bytes.Buffer
	command, e := clibootstrap.NewMainCommand(clibootstrap.MainCommandOptions{Arguments: []string{"auth", "login"}, Native: n, Platform: "darwin", Version: "999.0.0-local", PID: os.Getpid(), NonInteractive: true, Output: &goOut, Error: &goError})
	if e != nil {
		t.Fatal(e)
	}
	defer command.Close()
	oauth, e := desktopoauth.ResolveConfig(env)
	if e != nil {
		t.Fatal(e)
	}
	oauth.BaseAPIURL = peer.URL
	oauth.TokenURL = peer.URL + "/v1/oauth/token"
	var result cliauth.Result
	e = command.Run(ctx, func(scope *cooperative.Scope) error {
		var e error
		result, e = command.AuthLogin(ctx, scope, clibootstrap.AuthLoginOptions{Input: cliauth.LoginInput{Console: inputConsole, Email: func() string {
			if variant == "email-sso" {
				return "fixture+ü@example.invalid"
			}
			return ""
		}(), SSO: variant == "email-sso"}, Config: &oauth, HTTP: peer.Client(), Version: "999.0.0-local", Browser: func(ctx context.Context, address string) error {
			authorize, _ := url.Parse(address)
			callback, _ := url.Parse(authorize.Query().Get("redirect_uri"))
			q := callback.Query()
			q.Set("code", "fixture-authorization-code")
			q.Set("state", authorize.Query().Get("state"))
			callback.RawQuery = q.Encode()
			browser := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			r, _ := http.NewRequestWithContext(ctx, "GET", callback.String(), nil)
			response, e := browser.Do(r)
			if e != nil {
				return e
			}
			defer response.Body.Close()
			_, _ = io.Copy(io.Discard, response.Body)
			if response.StatusCode != 302 {
				return fmt.Errorf("actualcallbackstatus%d", response.StatusCode)
			}
			return nil
		}})
		return e
	})
	expectedStdout := "Login successful.\n"
	if sourceExit != 0 {
		expectedStdout = ""
	}
	if e != nil || result.ExitCode != sourceExit || refreshMode && result.Stdout != string(output) || !refreshMode && result.Stdout != expectedStdout || result.Stderr != stderr.String() {
		t.Fatal("actual same-owner Go login", e, result, goError.String())
	}
	goSecure, e := os.ReadFile(filepath.Join(config, ".credentials.json"))
	if expectStored && e != nil || !expectStored && variant != "storage-failure" && !os.IsNotExist(e) {
		t.Fatal(e)
	}
	var sourceRecord, goRecord map[string]any
	_ = json.Unmarshal(sourceSecure, &sourceRecord)
	_ = json.Unmarshal(goSecure, &goRecord)
	if expectStored {
		sourceTokens := sourceRecord["claudeAiOauth"].(map[string]any)
		goTokens := goRecord["claudeAiOauth"].(map[string]any)
		delete(sourceTokens, "expiresAt")
		delete(goTokens, "expiresAt")
		if !reflect.DeepEqual(sourceRecord, goRecord) {
			t.Fatalf("actual physical CLI token store Source=%v Go=%v", sourceRecord, goRecord)
		}
	} else if consoleMode && variant != "missing-console-key" {
		sourceInput, e := os.ReadFile(filepath.Join(home, "legacy-save-fixture.txt"))
		goInput, ge := os.ReadFile(filepath.Join(home, "go-legacy-save.txt"))
		if e != nil || ge != nil || !bytes.Equal(sourceInput, goInput) {
			t.Fatalf("actual secret stdin original legacy store Source=%q Go=%q errors=%v/%v", sourceInput, goInput, e, ge)
		}
	}
	goGlobalRaw, e := os.ReadFile(filepath.Join(config, ".claude.json"))
	if e != nil {
		t.Fatal(e)
	}
	var goGlobal map[string]any
	_ = json.Unmarshal(goGlobalRaw, &goGlobal)
	for _, key := range []string{"future", "primaryApiKey", "oauthAccount", "hasCompletedOnboarding", "claudeCodeFirstTokenDate", "customApiKeyResponses"} {
		if !reflect.DeepEqual(sourceGlobal[key], goGlobal[key]) {
			t.Errorf("actual global field %s Source=%v Go=%v", key, sourceGlobal[key], goGlobal[key])
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != sourceRequestCount*2 {
		t.Fatal("actual Source/Go physical request count", paths)
	}
	for i := 0; i < sourceRequestCount; i++ {
		if !reflect.DeepEqual(requestHeaders[i], requestHeaders[i+sourceRequestCount]) {
			t.Errorf("actual original auth request headers at %s Source=%v Go=%v", paths[i], requestHeaders[i], requestHeaders[i+sourceRequestCount])
		}
	}
	if variant == "policy-expired" {
		if !reflect.DeepEqual(leaseObservations, []bool{true, true}) {
			t.Fatal("actual Source/Go second token POST without physical lease", leaseObservations)
		}
		if _, e := os.Stat(config + ".lock"); !os.IsNotExist(e) {
			t.Fatal("Go did not release physical directory lease", e)
		}
	}
	t.Log("actual Source/Go physical client requests", paths)

}
