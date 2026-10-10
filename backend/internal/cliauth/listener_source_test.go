package cliauth_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliauth"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestActualSourceCLIBadStateRejectsWithoutTokenInstallation(t *testing.T) {
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	config := filepath.Join(home, ".claude")
	_ = os.Mkdir(config, 0700)
	_ = os.WriteFile(filepath.Join(config, "settings.json"), []byte("{}"), 0600)
	initial := []byte(`{"futureSecure":true}`)
	_ = os.WriteFile(filepath.Join(home, "secure-fixture.json"), initial, 0600)
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	fixture, _ := filepath.Abs("../../testdata/native-cli-auth-login.fixture.ts")
	profile, _ := filepath.Abs("../../testdata/cli-auth-loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "PATH=/usr/bin:/bin", "USER=fixture", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLI_LOGIN_FIXTURE=1", "CC_HAHA_CLI_LOGIN_PEER=https://127.0.0.1:1", "CC_HAHA_CLI_LOGIN_WRONG_STATE=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "USER_TYPE=external", "NODE_ENV=production"}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	e = command.Run()
	if e == nil || stderr.String() != "Login failed: Invalid state parameter\n" {
		t.Fatal("actual Source state rejection", e, stderr.String())
	}
	unchanged, e := os.ReadFile(filepath.Join(home, "secure-fixture.json"))
	if e != nil || !bytes.Equal(initial, unchanged) {
		t.Fatal("Source mutated secure on bad state", e)
	}
	listener := cliauth.NewOAuthListener("/callback")
	port, e := listener.Start(0)
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	callbackResult := make(chan string, 1)
	_, e = listener.Wait(ctx, "actual-state", func() error {
		response, e := http.Get("http://localhost:" + fmt.Sprint(port) + "/callback?code=fixture&state=wrong")
		if e != nil {
			return e
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		callbackResult <- string(body)
		return nil
	})
	if e == nil || e.Error() != "Invalid state parameter" {
		t.Fatal("actual Go state rejection", e)
	}
	if got := <-callbackResult; got != "Invalid state parameter" {
		t.Fatal(got)
	}
	listener.Close()
	_, e = http.Get("http://localhost:" + fmt.Sprint(port) + "/callback")
	if e == nil {
		t.Fatal("closed physical callback port remained accessible")
	}
}
func TestCLIListenerCancellationClosesPhysicalPendingServer(t *testing.T) {
	listener := cliauth.NewOAuthListener("/callback")
	port, e := listener.Start(0)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	go func() { <-started; cancel() }()
	_, e = listener.Wait(ctx, "owned-state", func() error { close(started); return nil })
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	listener.Close()
	_, e = http.Get("http://localhost:" + fmt.Sprint(port) + "/callback")
	if e == nil {
		t.Fatal("cancelled owned callback server remains listening")
	}
}
