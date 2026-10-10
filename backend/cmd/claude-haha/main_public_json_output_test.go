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
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualPublicCLIJSONCollectionAndEarlyValidationStderr(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	binary := filepath.Join(root, "actual-public-json-cli")
	build := exec.CommandContext(ctx, "go", "build", "-p=4", "-tags=native_process_fixture", "-o", binary, ".")
	if raw, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(raw))
	}
	profiles := []struct {
		name, stderr string
		args         []string
	}{
		{name: "verbose-json", args: []string{"--print", "--output-format", "json", "--verbose"}},
		{name: "partial-json", args: []string{"--print", "--output-format", "json", "--include-partial-messages"}, stderr: "Error: --include-partial-messages requires --print and --output-format=stream-json."},
		{name: "replay-json", args: []string{"--print", "--output-format", "json", "--replay-user-messages"}, stderr: "Error: --replay-user-messages requires both --input-format=stream-json and --output-format=stream-json.\n"},
		{name: "stream-input-json", args: []string{"--print", "--output-format", "json", "--input-format", "stream-json"}, stderr: "Error: --input-format=stream-json requires output-format=stream-json.\n"},
		{name: "replay-text-input", args: []string{"--print", "--output-format", "stream-json", "--verbose", "--replay-user-messages"}, stderr: "Error: --replay-user-messages requires both --input-format=stream-json and --output-format=stream-json.\n"},
		{name: "persistence-terminal", args: []string{"--no-session-persistence"}, stderr: "Error: --no-session-persistence can only be used with --print mode."},
	}
	for _, profile := range profiles {
		t.Run(profile.name, func(t *testing.T) {
			home := filepath.Join(root, profile.name)
			work, tmp := filepath.Join(home, "work"), filepath.Join(home, "tmp")
			for _, path := range []string{work, tmp} {
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			config := filepath.Join(home, ".claude")
			var calls atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				calls.Add(1)
				jsonSchemaCLIResponse(w, "actual-verbose-json-response", "", nil, "ACTUAL_PUBLIC_VERBOSE_JSON_DONE")
			}))
			defer peer.Close()
			args := []string{"--bare", "--no-computer-use", "--tools", "Read", "--model", "fixture-model", "--thinking", "disabled", "--system-prompt", "Actual verbose JSON CLI."}
			args = append(args, profile.args...)
			args = append(args, "actual private provider")
			command := exec.CommandContext(ctx, binary, args...)
			command.Dir = work
			command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + tmp, "CC_HAHA_NATIVE_FIXTURE_ROOT=" + root, "PATH=" + os.Getenv("PATH"), "NODE_ENV=production", "USER_TYPE=external", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "ANTHROPIC_API_KEY=private-verbose-fake", "ANTHROPIC_BASE_URL=" + peer.URL, "CLAUDE_CODE_MAX_RETRIES=0"}
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			err := command.Run()
			if profile.stderr != "" {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 1 || calls.Load() != 0 || stdout.Len() != 0 || stderr.String() != profile.stderr {
					t.Fatalf("validation changed real CLI output: err%v calls%d stdout%q stderr%q", err, calls.Load(), stdout.Bytes(), stderr.Bytes())
				}
				if _, e := os.Stat(config); !os.IsNotExist(e) {
					t.Fatal("rejected arguments initialized user configuration", e)
				}
				return
			}
			var messages []protocol.JSONObject
			if err != nil || calls.Load() != 1 || stderr.Len() != 0 || json.Unmarshal(stdout.Bytes(), &messages) != nil || len(messages) != 3 {
				t.Fatalf("public CLI did not collect its real output: err%v calls%d stdout%s stderr%s", err, calls.Load(), stdout.Bytes(), stderr.Bytes())
			}
			if messages[0].String("type") != "system" || messages[0].String("subtype") != "init" || messages[1].String("type") != "assistant" || messages[2].String("type") != "result" || messages[2].String("result") != "ACTUAL_PUBLIC_VERBOSE_JSON_DONE" {
				t.Fatal("public message sequence differs from actual original", stdout.String())
			}
		})
	}
}
