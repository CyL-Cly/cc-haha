package cliauth

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

func TestActualSourceSetupTokenOnlyInferenceYearAndNoCredentialStorage(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	home := filepath.Join(root, "home")
	config := filepath.Join(home, ".claude")
	temp := filepath.Join(root, "tmp")
	for _, p := range []string{home, config, temp} {
		os.MkdirAll(p, 0700)
	}
	os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"forceLoginMethod":"console","forceLoginOrgUUID":"forced-owned-org","future":{"keep":true}}`), 0600)
	secure := []byte(`{"existingSecret":"old-owned-opaque","unknown":{"keep":true}}`)
	os.WriteFile(filepath.Join(home, "secure-fixture.json"), secure, 0600)
	var mu sync.Mutex
	var paths []string
	var body map[string]any
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/oauth/token":
			json.NewDecoder(r.Body).Decode(&body)
			w.Write([]byte(`{"access_token":"FAKE_SETUP_SECRET_ONLY","refresh_token":"unused-fake-refresh","scope":"user:inference","expires_in":31536000}`))
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
	fixture, _ := filepath.Abs("../../testdata/native-cli-setup-token.fixture.ts")
	profile, _ := filepath.Abs("../../testdata/cli-auth-loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	driver, _ := filepath.Abs("../../testdata/setup-token-pty.py")
	cmd := exec.CommandContext(ctx, "python3", driver, "/usr/bin/sandbox-exec", "-f", profile, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 3 * time.Second
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + temp, "NODE_EXTRA_CA_CERTS=" + ca, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLI_LOGIN_FIXTURE=1", "CC_HAHA_CLI_LOGIN_PEER=" + peer.URL, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "USER_TYPE=external", "NODE_ENV=production", "TERM=xterm-256color"}
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if e = cmd.Run(); e != nil {
		t.Fatal("actual Source setup-token", e, stderr.String(), out.String())
	}
	if !strings.Contains(out.String(), "FAKE_SETUP_SECRET_ONLY") || strings.Contains(stderr.String(), "FAKE_SETUP_SECRET_ONLY") {
		t.Fatal("Source secret output boundary", out.String(), stderr.String())
	}
	if body["expires_in"] != float64(31536000) {
		t.Fatal("Source one-year exchange", body)
	}
	browserRaw, e := os.ReadFile(filepath.Join(home, "browser-fixture.json"))
	if e != nil {
		t.Fatal(e)
	}
	var browser map[string]string
	json.Unmarshal(browserRaw, &browser)
	address, _ := url.Parse(browser["url"])
	if address.Hostname() != "claude.com" || address.Query().Get("scope") != "user:inference" || address.Query().Get("orgUUID") != "forced-owned-org" {
		t.Fatal("Source setup method/org/scopes", address)
	}
	after, _ := os.ReadFile(filepath.Join(home, "secure-fixture.json"))
	if !bytes.Equal(after, secure) {
		t.Fatal("setup-token mutated existing secure storage")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 2 || paths[0] != "/v1/oauth/token" || paths[1] != "/api/oauth/profile" {
		t.Fatal("setup must not install/roles/verify organization", paths)
	}
}
