package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPublicSetupTokenInferenceSecretRemainsOnTerminalWithoutStoredLogin(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	home := filepath.Join(root, "home")
	config := filepath.Join(home, ".claude")
	tmp := filepath.Join(root, "tmp")
	for _, p := range []string{home, config, tmp} {
		os.MkdirAll(p, 0700)
	}
	original := []byte(`{"forceLoginMethod":"console","forceLoginOrgUUID":"forced-owned-org","future":{"keep":true}}`)
	os.WriteFile(filepath.Join(config, "settings.json"), original, 0600)
	credentials := []byte(`{"claudeAiOauth":{"accessToken":"OLD_FAKE_TOKEN","refreshToken":"OLD_FAKE_REFRESH","scopes":["user:profile"]},"unknown":{"keep":true}}`)
	os.WriteFile(filepath.Join(config, ".credentials.json"), credentials, 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	binary := filepath.Join(root, "go-cli")
	build := exec.CommandContext(ctx, "go", "build", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	if raw, e := build.CombinedOutput(); e != nil {
		t.Fatal(e, string(raw))
	}
	var mu sync.Mutex
	var paths []string
	var tokenBody map[string]any
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/oauth/token":
			json.NewDecoder(r.Body).Decode(&tokenBody)
			w.Write([]byte(`{"access_token":"FAKE_GO_SETUP_SECRET_ONLY","refresh_token":"unused-fake-refresh","scope":"user:inference","expires_in":31536000}`))
		case "/api/oauth/profile":
			w.WriteHeader(403)
			w.Write([]byte(`{"error":"no-profile-scope"}`))
		default:
			t.Errorf("unexpected setup request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer peer.Close()
	ca := filepath.Join(root, "ca.pem")
	os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}), 0600)
	driver, _ := filepath.Abs("../../testdata/setup-token-pty.py")
	profile, _ := filepath.Abs("../../testdata/cli-auth-loopback-only.sb")
	cmd := exec.CommandContext(ctx, "python3", driver, "/usr/bin/sandbox-exec", "-f", profile, binary, "setup-token")
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 3 * time.Second
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + tmp, "CC_HAHA_NATIVE_FIXTURE_ROOT=" + root, "CC_HAHA_CLI_LOGIN_PEER=" + peer.URL, "NODE_EXTRA_CA_CERTS=" + ca, "PATH=/usr/bin:/bin", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "USER_TYPE=external", "NODE_ENV=production", "TERM=xterm-256color"}
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if e = cmd.Run(); e != nil {
		t.Fatal("public setup-token", e, stderr.String(), out.String())
	}
	if !strings.Contains(out.String(), "FAKE_GO_SETUP_SECRET_ONLY") || strings.Contains(stderr.String(), "FAKE_GO_SETUP_SECRET_ONLY") || strings.Contains(out.String(), "\x1b[?1049h") {
		t.Fatal("public secret/scrollback boundary", out.String(), stderr.String())
	}
	if tokenBody["expires_in"] != float64(31536000) {
		t.Fatal("actual public year exchange", tokenBody)
	}
	raw, e := os.ReadFile(filepath.Join(root, "browser-fixture.json"))
	if e != nil {
		t.Fatal(e)
	}
	var browser map[string]string
	json.Unmarshal(raw, &browser)
	authorize, _ := url.Parse(browser["url"])
	if authorize.Hostname() != "claude.com" || authorize.Query().Get("scope") != "user:inference" || authorize.Query().Get("orgUUID") != "forced-owned-org" {
		t.Fatal("actual public method/scopes/org", authorize)
	}
	stored, _ := os.ReadFile(filepath.Join(config, ".credentials.json"))
	if !bytes.Equal(stored, credentials) {
		t.Fatal("setup-token replaced existing account")
	}
	settings, _ := os.ReadFile(filepath.Join(config, "settings.json"))
	if !bytes.Equal(settings, original) {
		t.Fatal("setup-token mutated settings")
	}
	if _, e = os.Stat(filepath.Join(config, "projects")); !os.IsNotExist(e) {
		t.Fatal("setup-token initialized Agent/ASK history")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 2 || paths[0] != "/v1/oauth/token" || paths[1] != "/api/oauth/profile" {
		t.Fatal("setup-token performed account installation", paths)
	}
}
