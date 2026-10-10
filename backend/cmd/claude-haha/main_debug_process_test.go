package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Public parser, native startup, real sink, shared Harness, SDK result and Close
// run in a child process. Only external managed/keychain primitives are isolated.
func TestPublicDebugCLIActualPrivateSharedHarnessFilesStderrAndClose(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	binary := filepath.Join(root, "actual-debug-cli")
	build := exec.CommandContext(ctx, "go", "build", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	if data, e := build.CombinedOutput(); e != nil {
		t.Fatal(e, string(data))
	}
	profiles := []struct {
		name                           string
		args                           []string
		env                            []string
		file, stderr, fail, latestFail bool
	}{
		{name: "file", args: []string{"--debug-file", "<FILE>"}, file: true},
		{name: "equals-file", args: []string{"--debug-file=<FILE>"}, file: true},
		{name: "long", args: []string{"--debug"}, file: true},
		{name: "short", args: []string{"-d"}, file: true},
		{name: "short-attached", args: []string{"-dapi"}},
		{name: "stderr", args: []string{"--debug-to-stderr"}, stderr: true},
		{name: "short-stderr", args: []string{"-d2e"}, stderr: true},
		{name: "file-stderr", args: []string{"--debug-file", "<FILE>", "--debug-to-stderr"}, stderr: true},
		{name: "first-file", args: []string{"--debug-file", "<FILE>", "--debug-file=<OTHER>"}, file: true},
		{name: "separate-filter", args: []string{"--debug", "missing"}, file: true},
		{name: "equals-filter", args: []string{"--debug=missing"}},
		{name: "default"},
		{name: "env", env: []string{"DEBUG=1"}, file: true},
		{name: "sdk-env", env: []string{"DEBUG_SDK=true"}, file: true},
		{name: "env-file", env: []string{"DEBUG=1", "CLAUDE_CODE_DEBUG_LOGS_DIR=<FILE>"}, file: true},
		{name: "test-disabled", args: []string{"--debug-file", "<FILE>"}, env: []string{"NODE_ENV=test"}},
		{name: "test-stderr", args: []string{"-d2e"}, env: []string{"NODE_ENV=test"}, stderr: true},
		{name: "error-min", args: []string{"--debug-file", "<FILE>"}, env: []string{"CLAUDE_CODE_DEBUG_LOG_LEVEL=error"}},
		{name: "append-failure", args: []string{"--debug-file", "<FILE>"}, fail: true},
		{name: "latest-failure", args: []string{"--debug-file", "<FILE>"}, file: true, latestFail: true},
	}
	for _, p := range profiles {
		t.Run(p.name, func(t *testing.T) {
			home := filepath.Join(root, p.name)
			config := filepath.Join(home, ".claude")
			work := filepath.Join(home, "work")
			tmp := filepath.Join(home, "tmp")
			for _, path := range []string{config, work, tmp} {
				if e := os.MkdirAll(path, 0700); e != nil {
					t.Fatal(e)
				}
			}
			file := filepath.Join(config, "custom", "debug.log")
			other := filepath.Join(config, "custom", "other.log")
			replace := strings.NewReplacer("<FILE>", file, "<OTHER>", other)
			if p.fail {
				if e := os.MkdirAll(file, 0700); e != nil {
					t.Fatal(e)
				}
			}
			if p.latestFail {
				if e := os.MkdirAll(filepath.Join(filepath.Dir(file), "latest"), 0700); e != nil {
					t.Fatal(e)
				}
			}
			var calls atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				calls.Add(1)
				jsonSchemaCLIResponse(w, "debug-private-response", "", nil, "ACTUAL_DEBUG_DONE")
			}))
			defer peer.Close()
			args := []string{"--bare", "-p", "--no-computer-use", "--model", "fixture-model", "--thinking", "disabled", "--system-prompt", "Actual debug CLI.", "--tools", "Read", "--output-format", "json", "--session-id", "12345678-1234-4234-8234-123456789012"}
			debugArgs := []string{}
			for _, arg := range p.args {
				debugArgs = append(debugArgs, replace.Replace(arg))
			}
			args = append(debugArgs, args...)
			args = append(args, "Return actual private response")
			env := []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + tmp, "CC_HAHA_NATIVE_FIXTURE_ROOT=" + root, "PATH=" + os.Getenv("PATH"), "USER_TYPE=external", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "ANTHROPIC_API_KEY=private-debug-fake", "ANTHROPIC_BASE_URL=" + peer.URL, "CLAUDE_CODE_MAX_RETRIES=0"}
			for _, v := range p.env {
				env = append(env, replace.Replace(v))
			}
			cmd := exec.CommandContext(ctx, binary, args...)
			cmd.Dir = work
			cmd.Env = env
			var out, stderr bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &stderr
			e := cmd.Run()
			if p.fail {
				var exit *exec.ExitError
				if !errors.As(e, &exit) || exit.ExitCode() != 1 || calls.Load() != 0 || out.Len() != 0 || !strings.Contains(stderr.String(), "directory") {
					t.Fatalf("immediate failure must abort startup before query: %v calls%d stdout%q stderr%q", e, calls.Load(), out.String(), stderr.String())
				}
				return
			}
			var result protocol.JSONObject
			if e != nil || calls.Load() != 1 || result.UnmarshalJSON(out.Bytes()) != nil || result.String("subtype") != "success" || result.String("result") != "ACTUAL_DEBUG_DONE" {
				t.Fatalf("public debug flags did not reach actual shared result: %v calls%d stdout%q stderr%q", e, calls.Load(), out.String(), stderr.String())
			}
			if p.stderr != strings.Contains(stderr.String(), "[DEBUG] Error log sink initialized\n") {
				t.Fatalf("stderr debug mismatch: %q", stderr.String())
			}
			if len(p.args) == 0 || !strings.Contains(strings.Join(p.args, " "), "debug-file") {
				if p.name != "env-file" {
					file = filepath.Join(config, "debug", "12345678-1234-4234-8234-123456789012.txt")
				}
			}
			data, err := os.ReadFile(file)
			if p.file {
				if err != nil || !bytes.Contains(data, []byte("[DEBUG] Error log sink initialized\n")) {
					t.Fatalf("actual native sink did not write/flush before Close: %v %q stderr%q", err, data, stderr.String())
				}
				target, err := os.Readlink(filepath.Join(filepath.Dir(file), "latest"))
				if p.latestFail {
					info, e := os.Stat(filepath.Join(filepath.Dir(file), "latest"))
					if e != nil || !info.IsDir() {
						t.Fatal("Source ignored failed latest update must preserve directory", e)
					}
				} else if err != nil || target != file {
					t.Fatalf("latest physical target %q %v", target, err)
				}
			} else if err == nil {
				t.Fatalf("Source disabled/file-stderr branch wrote file %q", data)
			}
			if _, err := os.Stat(other); err == nil {
				t.Fatal("later debug-file overrode Source first raw argv path")
			}
			// SDK stdout is a single JSON result, never a debug line or a second value.
			var arbitrary any
			if json.Unmarshal(out.Bytes(), &arbitrary) != nil {
				t.Fatal("debug contaminated stdout")
			}
		})
	}
}

func TestActualSourcePublicDebugCLIStartupFilesStderrAndSDKIsolation(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	profiles := []struct {
		name                           string
		args                           []string
		environment                    map[string]string
		file, stderr, fail, latestFail bool
	}{
		{name: "file", args: []string{"--debug-file", "<FILE>"}, file: true},
		{name: "first-file", args: []string{"--debug-file", "<FILE>", "--debug-file=<OTHER>"}, file: true},
		{name: "short-attached", args: []string{"-dapi"}},
		{name: "short-stderr", args: []string{"-d2e"}, stderr: true},
		{name: "separate-filter", args: []string{"--debug", "missing", "--debug-file", "<FILE>"}, file: true},
		{name: "equals-filter", args: []string{"--debug=missing", "--debug-file", "<FILE>"}},
		{name: "test-disabled", args: []string{"--debug-file", "<FILE>"}, environment: map[string]string{"NODE_ENV": "test"}},
		{name: "test-stderr", args: []string{"--debug-to-stderr"}, environment: map[string]string{"NODE_ENV": "test"}, stderr: true},
		{name: "error-min", args: []string{"--debug-file", "<FILE>"}, environment: map[string]string{"CLAUDE_CODE_DEBUG_LOG_LEVEL": "error"}},
		{name: "default"},
		{name: "append-failure", args: []string{"--debug-file", "<FILE>"}, fail: true},
		{name: "latest-failure", args: []string{"--debug-file", "<FILE>"}, file: true, latestFail: true},
	}
	for _, p := range profiles {
		t.Run(p.name, func(t *testing.T) {
			home := t.TempDir()
			tmp := filepath.Join(home, "tmp")
			if e := os.MkdirAll(tmp, 0700); e != nil {
				t.Fatal(e)
			}
			file := filepath.Join(home, "custom", "debug.log")
			other := filepath.Join(home, "custom", "other.log")
			if p.fail {
				if e := os.MkdirAll(file, 0700); e != nil {
					t.Fatal(e)
				}
			}
			if p.latestFail {
				if e := os.MkdirAll(filepath.Join(filepath.Dir(file), "latest"), 0700); e != nil {
					t.Fatal(e)
				}
			}
			args := append([]string{}, p.args...)
			replace := strings.NewReplacer("<FILE>", file, "<OTHER>", other)
			for i, arg := range args {
				args[i] = replace.Replace(arg)
			}
			input, _ := json.Marshal(map[string]any{"homeDir": home, "arguments": args, "environment": p.environment})
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-debug-cli.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + tmp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_DEBUG_CLI_FIXTURE=1", "CC_HAHA_DEBUG_CLI_INPUT=" + string(input), "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "CLAUDE_CODE_MAX_RETRIES=0", "CC_HAHA_DISABLE_TERMINAL_SHELL_ENV=1"}
			var out, stderr bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &stderr
			runError := cmd.Run()
			if runError != nil && !p.fail {
				t.Fatal(runError, out.String(), stderr.String())
			}
			raw, e := os.ReadFile(filepath.Join(home, "original-debug-cli-receipt.json"))
			if e != nil {
				t.Fatal(e)
			}
			var receipt struct {
				Code         int
				Requests     []json.RawMessage
				Capabilities struct{ Blocked []string }
			}
			if p.fail {
				if e = json.Unmarshal(raw, &receipt); e != nil || receipt.Code != 1 || len(receipt.Requests) != 0 || len(receipt.Capabilities.Blocked) != 0 || out.Len() != 0 || stderr.Len() == 0 {
					t.Fatalf("actual Source immediate append failure %v %.1500s stdout%q stderr%q", e, raw, out.String(), stderr.String())
				}
				return
			}
			if e = json.Unmarshal(raw, &receipt); e != nil || receipt.Code != 0 || len(receipt.Requests) != 1 || len(receipt.Capabilities.Blocked) != 0 {
				t.Fatalf("actual Source startup/query failed: %v %.1500s stderr%q", e, raw, stderr.String())
			}
			var result protocol.JSONObject
			if result.UnmarshalJSON(out.Bytes()) != nil || result.String("subtype") != "success" || result.String("result") != "ACTUAL_DEBUG_DONE" {
				t.Fatalf("actual Source SDK stdout contaminated: %q", out.String())
			}
			if strings.Contains(stderr.String(), "[DEBUG] Error log sink initialized\n") != p.stderr {
				t.Fatalf("actual Source stderr branch %q", stderr.String())
			}
			data, e := os.ReadFile(file)
			if p.file {
				if e != nil || !bytes.Contains(data, []byte("[DEBUG] Error log sink initialized\n")) {
					t.Fatalf("actual Source debug file missing %v %.1000s", e, data)
				}
			} else if e == nil && bytes.Contains(data, []byte("Error log sink initialized")) {
				t.Fatalf("actual Source filter disabled init unexpectedly emitted %.1000s", data)
			}
			if p.latestFail {
				info, e := os.Stat(filepath.Join(filepath.Dir(file), "latest"))
				if e != nil || !info.IsDir() {
					t.Fatal("actual Source ignored latest unlink/symlink failure changed directory", e)
				}
			}
			if _, e := os.Stat(other); e == nil {
				t.Fatal("actual Source did not retain first raw argv debug path")
			}
		})
	}
}
