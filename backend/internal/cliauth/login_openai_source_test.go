package cliauth_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
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

	"github.com/NanmiCoder/cc-haha/backend/internal/cliauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/testdata/keychainprefetchfixture"
)

type cliOpenAIRoute struct {
	peer      *url.URL
	transport http.RoundTripper
}

func (r cliOpenAIRoute) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	address := *request.URL
	address.Scheme = r.peer.Scheme
	address.Host = r.peer.Host
	clone.URL = &address
	return r.transport.RoundTrip(clone)
}
func TestActualSourceCLIOpenAILoginInstallAndPKCE(t *testing.T) { actualCLIOpenAI(t, "", false) }
func TestActualSourceCLIOpenAILoginCompetingAuthWarning(t *testing.T) {
	actualCLIOpenAI(t, "fixture-anthropic-token", false)
}
func TestActualSourceCLIOpenAISelectedFileAndMarker(t *testing.T) { actualCLIOpenAI(t, "", true) }
func TestActualSourceCLIOpenAIOverrideSources(t *testing.T) {
	for _, mode := range []string{"unapproved-api-key", "approved-api-key", "api-helper", "managed-token", "third-party", "oauth-env", "bare"} {
		t.Run(mode, func(t *testing.T) { actualCLIOpenAI(t, "", false, mode) })
	}
}
func actualCLIOpenAI(t *testing.T, competingToken string, fileMode bool, modes ...string) {
	mode := ""
	if len(modes) > 0 {
		mode = modes[0]
	}

	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	config := filepath.Join(home, ".claude")
	_ = os.Mkdir(config, 0700)
	settings := `{}`
	if mode == "api-helper" {
		settings = `{"apiKeyHelper":"never-execute-fixture-helper"}`
	}
	_ = os.WriteFile(filepath.Join(config, "settings.json"), []byte(settings), 0600)
	initialGlobal := `{"primaryApiKey":"old-fixture","oauthAccount":{"old":true},"future":{"keep":true}}`
	if mode == "approved-api-key" {
		initialGlobal = `{"customApiKeyResponses":{"approved":["fixture-api-key"],"rejected":[],"future":true},"future":{"keep":true}}`
	}
	initial := []byte(`{"claudeAiOauth":{"old":true},"openaiCodexOauth":{"old":true},"futureSecure":{"keep":true}}`)
	_ = os.WriteFile(filepath.Join(home, "secure-fixture.json"), initial, 0600)
	_ = os.WriteFile(filepath.Join(config, ".claude.json"), []byte(initialGlobal), 0600)
	var mu sync.Mutex
	forms := []url.Values{}
	jwt := "x." + base64.RawURLEncoding.EncodeToString([]byte(`{"email":"fixture@example.invalid","chatgpt_account_id":"fixture-openai-account"}`)) + ".y"
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			t.Error("unowned OpenAI token path", r.URL.Path)
		}
		_ = r.ParseForm()
		mu.Lock()
		forms = append(forms, r.PostForm)
		mu.Unlock()
		if r.Header.Get("Accept") != "application/json" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.Header.Get("User-Agent") != "codex-cli/0.157.0" {
			t.Error("actual original OpenAI token headers")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fixture-access", "refresh_token": "fixture-refresh", "id_token": jwt, "expires_in": 3600})
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
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, bun, "--no-env-file", fixture)
	cmd.Dir = home
	env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": config, "TMPDIR": home, "PATH": "/usr/bin:/bin", "USER": "fixture", "BUN_OPTIONS": "--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT": filepath.Dir(home), "CC_HAHA_CLI_LOGIN_FIXTURE": "1", "CC_HAHA_CLI_LOGIN_PEER": peer.URL, "CC_HAHA_CLI_LOGIN_OPTIONS": `{"openai":true}`, "NODE_EXTRA_CA_CERTS": cert, "USER_TYPE": "external", "NODE_ENV": "production", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH": "1"}
	if fileMode {
		env["OPENAI_CODEX_OAUTH_FILE"] = "selected-oai.json"
	}
	if competingToken != "" {
		env["ANTHROPIC_AUTH_TOKEN"] = competingToken
	}
	switch mode {
	case "unapproved-api-key", "approved-api-key":
		env["ANTHROPIC_API_KEY"] = "fixture-api-key"
	case "managed-token":
		env["ANTHROPIC_AUTH_TOKEN"] = "fixture-managed-token"
		env["CLAUDE_CODE_REMOTE"] = "1"
	case "third-party":
		env["CLAUDE_CODE_USE_BEDROCK"] = "1"
	case "oauth-env":
		env["CLAUDE_CODE_OAUTH_TOKEN"] = "fixture-oauth-token"
	case "bare":
		env["CLAUDE_CODE_SIMPLE"] = "1"
	}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatal("actual Source OpenAI", e, stderr.String())
	}
	if !strings.Contains(string(output), "OpenAI login successful.\n") {
		t.Fatal(string(output))
	}
	sourceRaw, e := os.ReadFile(filepath.Join(home, "secure-fixture.json"))
	if e != nil {
		t.Fatal(e)
	}
	var source map[string]any
	_ = json.Unmarshal(sourceRaw, &source)
	if source["claudeAiOauth"] != nil || source["futureSecure"] == nil {
		t.Fatal("Source OpenAI secure cleanup", string(sourceRaw))
	}
	var originalFileTokens map[string]any
	if fileMode {
		raw, e := os.ReadFile(filepath.Join(home, "selected-oai.json"))
		if e != nil {
			t.Fatal("Source physical selected file", e)
		}
		_ = json.Unmarshal(raw, &originalFileTokens)
		marker, e := os.ReadFile(filepath.Join(config, "cc-haha", "openai-oauth-file-backed"))
		if e != nil || string(marker) != "1\n" {
			t.Fatal("Source physical marker", e, string(marker))
		}
		_ = os.Remove(filepath.Join(home, "selected-oai.json"))
		_ = os.Remove(filepath.Join(config, "cc-haha", "openai-oauth-file-backed"))
	}
	browserRaw, e := os.ReadFile(filepath.Join(home, "browser-fixture.json"))
	if e != nil {
		t.Fatal(e)
	}
	var browser map[string]string
	_ = json.Unmarshal(browserRaw, &browser)
	sourceURL, _ := url.Parse(browser["url"])
	sourceCallbackRaw, e := os.ReadFile(filepath.Join(home, "callback-fixture.json"))
	if e != nil {
		t.Fatal("actual Source browser receipt", e)
	}
	var sourceCallback map[string]any
	_ = json.Unmarshal(sourceCallbackRaw, &sourceCallback)
	var goCallback map[string]any
	_ = os.WriteFile(filepath.Join(config, ".credentials.json"), initial, 0600)
	_ = os.WriteFile(filepath.Join(config, ".claude.json"), []byte(initialGlobal), 0600)
	n, e := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: home, Platform: "darwin", ManagedRoot: filepath.Join(home, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if e != nil {
		t.Fatal(e)
	}
	n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, errors.New("owned empty Keychain")
	}
	n.StartKeychainPrefetch = keychainprefetchfixture.Bind(n.Temp, n.CWD, n.Home, func(ctx context.Context, call securestorage.Command) ([]byte, error) {
		return n.RunSecurity(ctx, call)
	})
	var out, goError bytes.Buffer
	command, e := clibootstrap.NewMainCommand(clibootstrap.MainCommandOptions{Arguments: []string{"auth", "login", "--openai"}, Native: n, Platform: "darwin", Version: "999.0.0-local", PID: os.Getpid(), NonInteractive: true, Output: &out, Error: &goError})
	if e != nil {
		t.Fatal(e)
	}
	defer command.Close()
	client := peer.Client()
	address, _ := url.Parse(peer.URL)
	client.Transport = cliOpenAIRoute{address, client.Transport}
	var goURL *url.URL
	var result cliauth.Result
	e = command.Run(ctx, func(scope *cooperative.Scope) error {
		var e error
		result, e = command.AuthLogin(ctx, scope, clibootstrap.AuthLoginOptions{Input: cliauth.LoginInput{OpenAI: true}, HTTP: client, Browser: func(ctx context.Context, address string) error {
			goURL, _ = url.Parse(address)
			callback, _ := url.Parse(goURL.Query().Get("redirect_uri"))
			q := callback.Query()
			q.Set("code", "fixture-authorization-code")
			q.Set("state", goURL.Query().Get("state"))
			callback.RawQuery = q.Encode()
			browser := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			r, _ := http.NewRequestWithContext(context.WithoutCancel(ctx), "GET", callback.String(), nil)
			response, e := browser.Do(r)
			if e != nil {
				return e
			}
			defer response.Body.Close()
			raw, e := io.ReadAll(response.Body)
			if e != nil {
				return e
			}
			goCallback = map[string]any{"status": float64(response.StatusCode), "location": nil, "body": string(raw)}
			if response.StatusCode != 200 || !bytes.Contains(raw, []byte("Authorization Successful")) {
				return fmt.Errorf("actual OpenAI held callback reply differs")
			}
			return nil
		}})
		return e
	})
	if e != nil || result.ExitCode != 0 || !strings.HasSuffix(string(output), result.Stdout) {
		t.Fatal("actual Go OpenAI", e, result, goError.String(), "Source actual output", string(output))
	}
	if !reflect.DeepEqual(sourceCallback, goCallback) {
		t.Fatalf("actual Source full callback body %v Go %v", sourceCallback, goCallback)
	}
	raw, e := os.ReadFile(filepath.Join(config, ".credentials.json"))
	if e != nil {
		t.Fatal(e)
	}
	var goRecord map[string]any
	_ = json.Unmarshal(raw, &goRecord)
	if !fileMode {
		for _, record := range []map[string]any{source, goRecord} {
			delete(record["openaiCodexOauth"].(map[string]any), "expiresAt")
		}
	} else {
		sourceFile, e := os.ReadFile(filepath.Join(home, "selected-oai.json"))
		if e != nil {
			t.Fatal(e)
		}
		marker, e := os.ReadFile(filepath.Join(config, "cc-haha", "openai-oauth-file-backed"))
		if e != nil || string(marker) != "1\n" {
			t.Fatal("actual CLI file marker", e, string(marker))
		}
		var fileTokens map[string]any
		_ = json.Unmarshal(sourceFile, &fileTokens)
		delete(fileTokens, "expiresAt")
		delete(originalFileTokens, "expiresAt")
		if !reflect.DeepEqual(fileTokens, originalFileTokens) {
			t.Fatalf("actual Source/Go selected token file Source=%v Go=%v", originalFileTokens, fileTokens)
		}
		if fileTokens["refreshToken"] != "fixture-refresh" {
			t.Fatal("actual selected file token", string(sourceFile))
		}
	}
	if !reflect.DeepEqual(source, goRecord) {
		t.Fatalf("actual Source/Go OpenAI secure install Source=%v Go=%v", source, goRecord)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(forms) != 2 {
		t.Fatal("actual Source/Go OpenAI token requests", len(forms))
	}
	if len(forms[0].Get("code_verifier")) != 128 || len(forms[1].Get("code_verifier")) != 128 || len(sourceURL.Query().Get("state")) != 64 || len(goURL.Query().Get("state")) != 64 {
		t.Fatal("actual Source OpenAI crypto shape differs", len(forms[0].Get("code_verifier")), len(forms[1].Get("code_verifier")), len(sourceURL.Query().Get("state")), len(goURL.Query().Get("state")))
	}
}
