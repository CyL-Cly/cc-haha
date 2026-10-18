//go:build !offline_native_fixture && !native_process_fixture

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliargs"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func privateOrdinarySmokeBoundary(t *testing.T) (*clibootstrap.NativeBoundary, map[string]string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	home, cwd := filepath.Join(root, "home"), filepath.Join(root, "project")
	config, temporary, managed := filepath.Join(home, ".claude"), filepath.Join(root, "tmp"), filepath.Join(root, "managed")
	for _, path := range []string{config, cwd, temporary, managed} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"future":{"owned":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"migrationVersion":11}`), 0600); err != nil {
		t.Fatal(err)
	}
	environment := map[string]string{"HOME": home, "USERPROFILE": home, "USER": "owned-smoke", "CLAUDE_CONFIG_DIR": config, "TMPDIR": temporary, "TMP": temporary, "TEMP": temporary, "CC_HAHA_ELECTRON_SMOKE_STATE_ROOT": root, "CC_HAHA_ELECTRON_WINDOW_SMOKE_LOG": filepath.Join(root, "window.ndjson"), "SystemRoot": os.Getenv("SystemRoot"), "PATH": "/usr/bin:/bin", "ANTHROPIC_BASE_URL": "http://127.0.0.1:1", "ANTHROPIC_API_KEY": "fake-smoke-only", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH": "1", "GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_SYSTEM": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1"}
	// This is the ordinary, untagged factory. Its credential binding must be
	// supplied by the validated Smoke profile, not a test-only Native adapter.
	native, err := newNativeBoundaryForEnvironment(environment, cwd, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	// Substitute only the existing external policy primitive before any
	// constructor reads settings. No machine MDM or managed files are consulted.
	native.ManagedRoot, native.Temp = managed, temporary
	native.ResolvePolicy = func() (*protocol.JSONObject, error) { return nil, nil }
	return native, environment
}

func TestOrdinarySmokeBoundaryConstructsNonBareMainCommandWithActualPrefetch(t *testing.T) {
	native, environment := privateOrdinarySmokeBoundary(t)
	if native.RunSecurity == nil {
		t.Fatal("validated Smoke profile lost sync credential isolation")
	}
	var processes []*securestorage.KeychainPrefetchProcess
	if start := native.StartKeychainPrefetch; start != nil {
		native.StartKeychainPrefetch = func(ctx context.Context, call securestorage.Command) (*securestorage.KeychainPrefetchProcess, error) {
			process, err := start(ctx, call)
			if err == nil {
				processes = append(processes, process)
			}
			return process, err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command, err := clibootstrap.NewMainCommand(clibootstrap.MainCommandOptions{Context: ctx, Arguments: []string{"--setting-sources", "user", "plugin", "marketplace", "list", "--json"}, Native: native, Platform: "darwin", PID: os.Getpid(), Version: "owned-smoke", NonInteractive: true, Output: io.Discard, Error: io.Discard})
	if err != nil {
		t.Fatal("ordinary Smoke MainCommand rejected its missing prefetch primitive", err)
	}
	defer command.Close()
	if native.Environment["CLAUDE_CODE_SIMPLE"] != "" || len(processes) != 2 {
		t.Fatal("nonbare startup bypassed the actual two-read producer", len(processes), native.Environment["CLAUDE_CODE_SIMPLE"])
	}
	for _, process := range processes {
		if process.PID() <= 0 {
			t.Fatal("Smoke admitted a fabricated process")
		}
		select {
		case <-process.Done():
		default:
			t.Fatal("MainCommand returned before actual prefetch Wait", process.PID())
		}
	}
	if value, err := command.Secure.ReadSourceJSON(ctx); err != nil || string(value) != "{}" {
		t.Fatal("Smoke read did not retain empty secure-store semantics", string(value), err)
	}
	for _, operation := range []string{"find-generic-password", "add-generic-password", "delete-generic-password"} {
		if value, err := native.RunSecurity(ctx, securestorage.Command{Args: []string{operation}}); len(value) != 0 || !errors.Is(err, os.ErrPermission) {
			t.Fatal("Smoke sync permissions changed", operation, string(value), err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(environment["CLAUDE_CONFIG_DIR"], "settings.json")); err != nil || string(data) != `{"future":{"owned":true}}` {
		t.Fatal("construction changed private user settings", string(data), err)
	}
}

func TestOrdinarySmokeBoundaryKeepsNonSmokeAndUnboundCustomReadsFailClosed(t *testing.T) {
	native, environment := privateOrdinarySmokeBoundary(t)
	delete(environment, "CC_HAHA_ELECTRON_SMOKE_STATE_ROOT")
	delete(environment, "CC_HAHA_ELECTRON_WINDOW_SMOKE_LOG")
	ordinary, err := newNativeBoundaryForEnvironment(environment, native.CWD, "darwin")
	if err != nil || ordinary == nil || ordinary.RunSecurity != nil || ordinary.StartKeychainPrefetch != nil {
		t.Fatal("ordinary non-smoke native primitive changed", err)
	}
	// Deliberately do not construct the ordinary boundary: that would admit
	// the real credential primitive. Only the already-isolated Smoke is used.
	native.StartKeychainPrefetch = nil
	reads := 0
	read := native.RunSecurity
	native.RunSecurity = func(ctx context.Context, call securestorage.Command) ([]byte, error) {
		reads++
		return read(ctx, call)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command, err := clibootstrap.NewMainCommand(clibootstrap.MainCommandOptions{Context: ctx, Arguments: []string{"--setting-sources", "user", "plugin", "marketplace", "list", "--json"}, Native: native, Platform: "darwin", PID: os.Getpid(), Version: "owned-smoke", NonInteractive: true, Output: io.Discard, Error: io.Discard})
	if command != nil {
		command.Close()
		t.Fatal("custom read acquired a default real-Keychain prefetch")
	}
	if err == nil || !strings.Contains(err.Error(), "actual subprocess admission primitive") || reads != 0 {
		t.Fatal("unbound custom read did not fail before any credential call", reads, err)
	}
}

func TestOrdinaryWindowsSmokeCommandDoesNotRequireUnusedPrefetchRuntime(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows ordinary Smoke MainCommand without macOS prefetch")
	}
	private, environment := privateOrdinarySmokeBoundary(t)
	delete(environment, "SystemRoot")
	delete(environment, "SYSTEMROOT")
	native, err := newNativeBoundaryForEnvironment(environment, private.CWD, "windows")
	if err != nil || native == nil {
		t.Fatal("unused macOS prefetch rejected valid Windows Smoke boundary", err)
	}
	native.ManagedRoot, native.Temp, native.ResolvePolicy = private.ManagedRoot, private.Temp, private.ResolvePolicy
	if native.RunSecurity == nil || native.StartKeychainPrefetch == nil {
		t.Fatal("Smoke external credential isolation was lost")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command, err := clibootstrap.NewMainCommand(clibootstrap.MainCommandOptions{Context: ctx, Arguments: []string{"--setting-sources", "user", "plugin", "marketplace", "list", "--json"}, Native: native, Platform: "windows", PID: os.Getpid(), Version: "owned-smoke", NonInteractive: true, Output: io.Discard, Error: io.Discard})
	if err != nil {
		t.Fatal("Windows command required an unused prefetch executable", err)
	}
	defer command.Close()
	call := securestorage.Command{Args: []string{"find-generic-password", "-a", "owned-smoke", "-w", "-s", "owned-service"}}
	if process, err := native.StartKeychainPrefetch(ctx, call); process != nil || err == nil || !strings.Contains(err.Error(), "absolute Windows system runtime") {
		t.Fatal("Windows callback fell back to a real credential executable", process, err)
	}
}

func TestOrdinarySmokePluginCallerCompletesWithPrivateNativeBoundary(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS ordinary MainCommand Keychain prefetch caller")
	}
	native, environment := privateOrdinarySmokeBoundary(t)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("unexpected model/network operation: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer peer.Close()
	native.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	arguments := []string{"--setting-sources", "user", "plugin", "marketplace", "list", "--json"}
	parsed := cliargs.Parse(arguments, cliargs.Config{Version: version})
	// The parser's guarded action returns 86; main dispatches its nonnil
	// Parsed to the actual plugin handler, which supplies the final exit code.
	if parsed.ExitCode != 86 || parsed.Parsed == nil {
		t.Fatal("actual plugin caller parse failed", parsed.ExitCode, parsed.Parsed, parsed.Stdout, parsed.Stderr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var output, diagnostics bytes.Buffer
	code := runNativePluginCommand(ctx, arguments, parsed.Parsed, native, &output, &diagnostics)
	if code != 0 || output.String() != "[]\n" || diagnostics.Len() != 0 {
		t.Fatal("ordinary Smoke plugin preAction/action failed", code, output.String(), diagnostics.String())
	}
	if value, err := native.RunSecurity(ctx, securestorage.Command{Args: []string{"find-generic-password"}}); len(value) != 0 || !errors.Is(err, os.ErrPermission) {
		t.Fatal("public action replaced Smoke credential isolation", string(value), err)
	}
	if data, err := os.ReadFile(filepath.Join(environment["CLAUDE_CONFIG_DIR"], "settings.json")); err != nil || string(data) != `{"future":{"owned":true}}` {
		t.Fatal("private plugin list changed protected user settings", string(data), err)
	}
}
