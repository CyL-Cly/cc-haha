package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPublicPluginProgressSIGTERMClosesOwnedHTTPAndRestarts(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	config := filepath.Join(home, ".claude")
	if err = os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	binary := filepath.Join(root, "public-go-cli")
	build := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	if data, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(data))
	}
	entered, closed := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(closed) }))
	defer peer.Close()
	env := []string{"HOME=" + home, "USERPROFILE=" + home, "USER=owned", "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_NATIVE_FIXTURE_ROOT=" + root, "PATH=/usr/bin:/bin", "USER_TYPE=external", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1"}
	// Read the actual original handler's physical stdout while its actual Axios request is held.
	sourceEntered, sourceClosed := make(chan struct{}), make(chan struct{})
	sourcePeer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(sourceEntered)
		<-r.Context().Done()
		close(sourceClosed)
	}))
	fixture, _ := filepath.Abs("../../testdata/native-cli-plugins.fixture.ts")
	profile, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	input, _ := json.Marshal(map[string]any{"action": "marketplaceAdd", "argument": sourcePeer.URL + "/marketplace.json", "options": map[string]any{}})
	original := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	original.Dir = home
	original.Env = append(append([]string{}, env...), "CC_HAHA_FIXTURE_TEMP_ROOT="+root, "CC_HAHA_PLUGIN_CLI_FIXTURE=1", "CC_HAHA_PLUGIN_CLI_INPUT="+string(input), "BUN_OPTIONS=--no-env-file")
	originalStdout, e := original.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	var originalDiagnostics bytes.Buffer
	original.Stderr = &originalDiagnostics
	if e = original.Start(); e != nil {
		t.Fatal(e)
	}
	originalDone := make(chan struct{})
	go func() { _ = original.Wait(); close(originalDone) }()
	defer func() {
		select {
		case <-originalDone:
		default:
			_ = original.Process.Kill()
			<-originalDone
		}
		sourcePeer.Close()
	}()
	originalLines := make(chan string, 4)
	go func() {
		scanner := bufio.NewScanner(originalStdout)
		for scanner.Scan() {
			originalLines <- scanner.Text()
		}
		close(originalLines)
	}()
	select {
	case <-sourceEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("original physical HTTP was not reached")
	}
	for _, want := range []string{"Adding marketplace...", "Downloading marketplace from " + sourcePeer.URL + "/marketplace.json"} {
		select {
		case line := <-originalLines:
			if line != want {
				t.Fatalf("actual Source early stdout got%q want%q", line, want)
			}
		case <-time.After(time.Second):
			t.Fatal("original physical progress absent")
		}
	}
	_ = original.Process.Signal(syscall.SIGTERM)
	select {
	case <-originalDone:
	case <-time.After(5 * time.Second):
		t.Fatal("original owned fixture process did not terminate")
	}
	select {
	case <-sourceClosed:
	case <-time.After(time.Second):
		t.Fatal("original fixture socket survived termination")
	}
	process := exec.CommandContext(ctx, binary, "plugin", "marketplace", "add", peer.URL+"/marketplace.json")
	process.Dir = home
	process.Env = env
	stdout, err := process.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var diagnostics bytes.Buffer
	process.Stderr = &diagnostics
	if err = process.Start(); err != nil {
		t.Fatal(err)
	}
	lines := make(chan string, 4)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	waited := make(chan error, 1)
	done := make(chan struct{})
	go func() { waited <- process.Wait(); close(done) }()
	defer func() {
		select {
		case <-done:
		default:
			_ = process.Process.Kill()
			<-done
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("public actual marketplace request did not start")
	}
	select {
	case line := <-lines:
		if line != "Adding marketplace..." {
			t.Fatal("public first original progress", line)
		}
	case <-time.After(time.Second):
		_ = process.Process.Signal(syscall.SIGTERM)
		<-waited
		t.Fatal("public progress was buffered past actual HTTP request; original handler prints before await")
	}
	select {
	case line := <-lines:
		if line != "Downloading marketplace from "+peer.URL+"/marketplace.json" {
			t.Fatal("public download progress", line)
		}
	case <-time.After(time.Second):
		t.Fatal("public download progress absent")
	}
	if err = process.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-waited:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			t.Fatal("public canceled exit", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("public SIGTERM failed to close/join")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("actual marketplace HTTP survived public process exit")
	}
	if !strings.Contains(diagnostics.String(), "context canceled") {
		t.Fatal("public cancel diagnostics", diagnostics.String())
	}
	if _, err = os.Stat(filepath.Join(config, "plugins/known_marketplaces.json")); !os.IsNotExist(err) {
		t.Fatal("canceled request installed a marketplace", err)
	}
	restart := exec.CommandContext(ctx, binary, "plugin", "marketplace", "list", "--json")
	restart.Dir = home
	restart.Env = env
	data, err := restart.Output()
	if err != nil || string(data) != "[]\n" {
		t.Fatal("fresh CLI restart", err, string(data))
	}
}
