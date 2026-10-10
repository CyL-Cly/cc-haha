package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublicCLIAuthLoginRefreshMustReachOriginalInstallation(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	home := filepath.Join(root, "home")
	config := filepath.Join(home, ".claude")
	tmp := filepath.Join(root, "tmp")
	for _, dir := range []string{home, config, tmp} {
		if e = os.MkdirAll(dir, 0700); e != nil {
			t.Fatal(e)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	binary := filepath.Join(root, "go-cli")
	build := exec.CommandContext(ctx, "go", "build", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	var logs bytes.Buffer
	build.Stdout = &logs
	build.Stderr = &logs
	if e = build.Run(); e != nil {
		t.Fatal(e, logs.String())
	}
	command := exec.CommandContext(ctx, binary, "auth", "login")
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + tmp, "CC_HAHA_NATIVE_FIXTURE_ROOT=" + root, "PATH=/usr/bin:/bin", "USER=fixture", "USER_TYPE=external", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "CLAUDE_CODE_OAUTH_REFRESH_TOKEN=fixture-refresh"}
	var out, stderr bytes.Buffer
	command.Stdout = &out
	command.Stderr = &stderr
	e = command.Run()
	expected := "CLAUDE_CODE_OAUTH_SCOPES is required when using CLAUDE_CODE_OAUTH_REFRESH_TOKEN.\nSet it to the space-separated scopes the refresh token was issued with\n(e.g. \"user:inference\" or \"user:profile user:inference user:sessions:claude_code user:mcp_servers\").\n"
	if e == nil || out.Len() != 0 || stderr.String() != expected {
		t.Fatalf("ordinary public CLI exact original refresh preflight: error=%v stdout=%q stderr=%q", e, out.String(), stderr.String())
	}
	var requests atomic.Int32
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/oauth/token":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["grant_type"] == "refresh_token" && body["refresh_token"] != "fixture-refresh" || body["grant_type"] == "authorization_code" && body["code"] != "fixture-authorization-code" {
				t.Error("actual public CLI refresh token producer")
			}
			scope := "user:profile user:inference"
			_, _ = w.Write([]byte(`{"access_token":"fixture-public-access","refresh_token":"fixture-public-refresh","expires_in":3600,"scope":"` + scope + `"}`))
		case "/oauth/token":
			_, _ = w.Write([]byte(`{"access_token":"fixture-oai-public","refresh_token":"fixture-oai-refresh","expires_in":3600}`))
		case "/api/oauth/profile":
			_, _ = w.Write([]byte(`{"account":{"uuid":"fixture-public-account","email":"public@example.invalid","created_at":"2024-01-01"},"organization":{"uuid":"fixture-public-org","organization_type":"claude_pro","rate_limit_tier":"default_claude_pro"}}`))
		case "/api/oauth/claude_cli/roles":
			_, _ = w.Write([]byte(`{"organization_role":"owner","workspace_role":"admin","organization_name":"Public Fixture"}`))
		case "/api/organization/claude_code_first_token_date":
			_, _ = w.Write([]byte(`{"first_token_date":null}`))
		default:
			t.Error("unowned actual public OAuth path", r.URL.Path)
			http.Error(w, "unowned", 404)
		}
	}))
	defer peer.Close()
	cert := filepath.Join(home, "cert.pem")
	_ = os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}), 0600)
	_ = os.WriteFile(filepath.Join(config, ".claude.json"), []byte(`{"future":{"keep":true},"primaryApiKey":"old-fixture","hasCompletedOnboarding":false}`), 0600)
	actual := exec.CommandContext(ctx, binary, "auth", "login")
	actual.Dir = home
	actual.Env = append(append([]string{}, command.Env...), "CLAUDE_CODE_OAUTH_SCOPES=user:profile user:inference", "CC_HAHA_CLI_LOGIN_PEER="+peer.URL, "NODE_EXTRA_CA_CERTS="+cert)
	var actualOut, actualError bytes.Buffer
	actual.Stdout = &actualOut
	actual.Stderr = &actualError
	if e = actual.Run(); e != nil || actualOut.String() != "Login successful.\n" || actualError.Len() != 0 {
		t.Fatal("actual public CLI physical OAuth installation", e, actualOut.String(), actualError.String())
	}
	if requests.Load() != 4 {
		t.Fatal("actual public CLI physical TLS requests", requests.Load())
	}
	secure, e := os.ReadFile(filepath.Join(config, ".credentials.json"))
	if e != nil || !strings.Contains(string(secure), "fixture-public-refresh") {
		t.Fatal("actual public CLI physical token", e, string(secure))
	}
	global, e := os.ReadFile(filepath.Join(config, ".claude.json"))
	if e != nil {
		t.Fatal(e)
	}
	var fields map[string]any
	_ = json.Unmarshal(global, &fields)
	if fields["future"] == nil || fields["primaryApiKey"] != nil || fields["hasCompletedOnboarding"] != true {
		t.Fatal("actual public CLI preserved global state", string(global))
	}

	for _, provider := range []string{"claude", "openai"} {
		t.Run(provider, func(t *testing.T) {
			_ = os.Remove(filepath.Join(config, ".credentials.json"))
			_ = os.WriteFile(filepath.Join(config, ".claude.json"), []byte(`{"future":{"keep":true}}`), 0600)
			args := []string{"auth", "login"}
			if provider == "openai" {
				args = append(args, "--openai")
			}
			process := exec.CommandContext(ctx, binary, args...)
			process.Dir = home
			for _, pair := range actual.Env {
				if !strings.HasPrefix(pair, "CLAUDE_CODE_OAUTH_REFRESH_TOKEN=") && !strings.HasPrefix(pair, "CLAUDE_CODE_OAUTH_SCOPES=") {
					process.Env = append(process.Env, pair)
				}
			}
			if provider == "openai" {
				process.Env = append(process.Env, "OPENAI_CODEX_OAUTH_FILE=selected-public-openai.json")
			}
			var output, diagnostics bytes.Buffer
			process.Stdout = &output
			process.Stderr = &diagnostics
			if e = process.Run(); e != nil || diagnostics.Len() != 0 {
				t.Fatal("ordinary public browser login", e, output.String(), diagnostics.String())
			}
			if !strings.Contains(output.String(), "login successful.") && !strings.Contains(output.String(), "Login successful.") {
				t.Fatal(output.String())
			}
			raw, e := os.ReadFile(filepath.Join(root, "callback-fixture.json"))
			if e != nil {
				t.Fatal("actual public callback receipt", e)
			}
			var callback map[string]any
			_ = json.Unmarshal(raw, &callback)
			status := float64(302)
			if provider == "openai" {
				status = 200
			}
			if callback["status"] != status {
				t.Fatal("actual public browser callback", string(raw))
			}
			path := filepath.Join(config, ".credentials.json")
			token := "fixture-public-refresh"
			if provider == "openai" {
				path = filepath.Join(home, "selected-public-openai.json")
				token = "fixture-oai-refresh"
			}
			raw, e = os.ReadFile(path)
			if e != nil || !strings.Contains(string(raw), token) {
				t.Fatal("physical public CLI installation", e, string(raw))
			}
			browserRaw, e := os.ReadFile(filepath.Join(root, "browser-fixture.json"))
			if e != nil {
				t.Fatal(e)
			}
			var browser map[string]string
			_ = json.Unmarshal(browserRaw, &browser)
			callbackURL, _ := url.Parse(browser["callback"])
			connection, e := net.DialTimeout("tcp", callbackURL.Host, time.Second)
			if e == nil {
				connection.Close()
				t.Fatal("public CLI exit left callback server open")
			}
			// Public signal cancellation has a real waiting localhost listener and no
			// provider request: only the external opener waits for its supplied context.
			_ = os.Remove(filepath.Join(root, "browser-fixture.json"))
			cancelled := exec.CommandContext(ctx, binary, args...)
			cancelled.Dir = home
			cancelled.Env = append(append([]string{}, process.Env...), "CC_HAHA_CLI_LOGIN_HOLD_BROWSER=1")
			pipe, e := cancelled.StdoutPipe()
			if e != nil {
				t.Fatal(e)
			}
			var cancelError bytes.Buffer
			cancelled.Stderr = &cancelError
			if e = cancelled.Start(); e != nil {
				t.Fatal(e)
			}
			if e = readPublicLoginReady(pipe); e != nil {
				cancelled.Process.Kill()
				cancelled.Wait()
				t.Fatal(e)
			}
			browserRaw, e = os.ReadFile(filepath.Join(root, "browser-fixture.json"))
			if e != nil {
				cancelled.Process.Kill()
				cancelled.Wait()
				t.Fatal(e)
			}
			_ = json.Unmarshal(browserRaw, &browser)
			callbackURL, _ = url.Parse(browser["callback"])
			connection, e = net.DialTimeout("tcp", callbackURL.Host, time.Second)
			if e != nil {
				t.Fatal("public pending physical port", e)
			}
			connection.Close()
			if e = cancelled.Process.Signal(os.Interrupt); e != nil {
				t.Fatal(e)
			}
			if e = cancelled.Wait(); e == nil {
				t.Fatal("cancelled public auth returned success")
			}
			connection, e = net.DialTimeout("tcp", callbackURL.Host, time.Second)
			if e == nil {
				connection.Close()
				t.Fatal("signal exit left callback listener")
			}
			if _, e = os.Stat(path); e != nil {
				t.Fatal("cancel removed earlier stored login", e)
			}
			t.Log("ordinary public", provider, "physical token installation, callback, signal exit and port close")
		})
	}

}

func readPublicLoginReady(reader io.Reader) error {
	buf := make([]byte, 1)
	text := ""
	for strings.Count(text, "\n") < 3 {
		n, e := reader.Read(buf)
		if n > 0 {
			text += string(buf[:n])
		}
		if e != nil {
			return e
		}
	}
	return nil
}
