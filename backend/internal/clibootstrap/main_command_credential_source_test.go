package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestNativeCommandAuthStatusOriginalFDRemoteAndHomespace(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	profiles := []map[string]any{
		{"env": map[string]string{"CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR": "3"}, "credentialFiles": map[string]string{"descriptor-3": " \ufefffake-oauth-fd\u00a0\n"}},
		{"env": map[string]string{"CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR": "  +3tail"}, "credentialFiles": map[string]string{"descriptor-3": "fake-api-fd"}},
		{"env": map[string]string{"CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR": "3", "CLAUDE_CODE_REMOTE": "1", "ANTHROPIC_AUTH_TOKEN": "fake-ignored"}, "credentialFiles": map[string]string{"descriptor-3": "fake-remote-fd"}},
		{"env": map[string]string{"CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR": "3", "CI": "1"}, "credentialFiles": map[string]string{"descriptor-3": "fake-ci-api"}},
		{"env": map[string]string{"CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR": "3", "CI": "1"}, "credentialFiles": map[string]string{"descriptor-3": "fake-ci-oauth"}},
		{"env": map[string]string{"CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR": "bad", "CLAUDE_CODE_OAUTH_TOKEN": "fake-priority"}, "credentialFiles": map[string]string{}},
		{"env": map[string]string{"CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR": "3", "CLAUDE_CODE_SIMPLE": "1"}, "credentialFiles": map[string]string{"descriptor-3": "fake-ignored-bare"}},
		{"env": map[string]string{"CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR": "3", "ANTHROPIC_API_KEY": "fake-env-api"}, "nonInteractive": true, "credentialFiles": map[string]string{"descriptor-3": "fake-fd-low-priority"}},
		{"env": map[string]string{"USER_TYPE": "ant", "COO_RUNNING_ON_HOMESPACE": "1", "ANTHROPIC_API_KEY": "fake-ignored-homespace"}, "nonInteractive": true, "legacy": "fake-console-key", "credentialFiles": map[string]string{}},
		{"env": map[string]string{"CLAUDE_CODE_REMOTE": "1", "ANTHROPIC_AUTH_TOKEN": "fake-ignored"}, "secure": map[string]any{"claudeAiOauth": map[string]any{"accessToken": "fake-secure", "scopes": []string{"user:inference"}, "subscriptionType": "pro"}}, "credentialFiles": map[string]string{}},
		{"env": map[string]string{}, "credentialFiles": map[string]string{".oauth_token": " fake-fallback-oauth\n"}},
		{"env": map[string]string{}, "credentialFiles": map[string]string{".api_key": " fake-fallback-api\n"}},
		{"env": map[string]string{"CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR": "99"}, "credentialFiles": map[string]string{".oauth_token": "fake-failed-fd-fallback"}},
		{"env": map[string]string{"CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR": "invalid"}, "credentialFiles": map[string]string{".api_key": "fake-ignored-invalid-fallback"}},
		{"env": map[string]string{"CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR": "3"}, "credentialFiles": map[string]string{"descriptor-3": " \n", ".oauth_token": "fake-ignored-empty-fallback"}},
		{"env": map[string]string{"CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR": "3", "CLAUDE_CODE_REMOTE": "1"}, "credentialFiles": map[string]string{"descriptor-3": "fake-remote-api"}},
		{"env": map[string]string{"CLAUDE_CODE_SIMPLE": "1", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR": "3"}, "credentialFiles": map[string]string{"descriptor-3": "fake-ignored-bare", ".oauth_token": "fake-ignored-bare-fallback", ".api_key": "fake-ignored-bare-api"}},
		{"env": map[string]string{"USER_TYPE": "ant", "COO_RUNNING_ON_HOMESPACE": "1", "CLAUDE_CODE_SIMPLE": "1", "ANTHROPIC_API_KEY": "fake-bare-env"}, "credentialFiles": map[string]string{}},
		{"env": map[string]string{"CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR": strings.Repeat("9", 400)}, "credentialFiles": map[string]string{".api_key": "fake-overflow-fallback"}},
		{"env": map[string]string{"CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR": "9007199254740993suffix"}, "credentialFiles": map[string]string{".oauth_token": "fake-rounded-fallback"}},
	}
	rows := []map[string]any{}
	for _, profile := range profiles {
		for _, text := range []bool{false, true} {
			row := map[string]any{}
			for k, v := range profile {
				row[k] = v
			}
			row["text"] = text
			rows = append(rows, row)
		}
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-command-auth-status.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_COMMAND_AUTH_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(rows))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source status/FD: %v %s", err, stderr.String())
	}
	var expected []struct {
		Stdout, Stderr string
		ExitCode       int
		Failure        any
		Persisted      map[string]string
	}
	if json.Unmarshal(data, &expected) != nil || len(expected) != len(rows) {
		t.Fatalf("Source shape %s", data)
	}
	for i, row := range rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			fixtureRow := map[string]any{}
			for k, v := range row {
				fixtureRow[k] = v
			}
			fixtureEnv := map[string]string{}
			for k, v := range row["env"].(map[string]string) {
				fixtureEnv[k] = v
			}
			// The auth consumer's homespace behavior is independent of the
			// unresolved internal-only settings schema bootstrap.
			fixtureEnv["USER_TYPE"] = "external"
			fixtureRow["env"] = fixtureEnv
			m := authCommandFixture(t, fixtureRow)
			m.Native.Environment["USER_TYPE"] = row["env"].(map[string]string)["USER_TYPE"]
			for _, key := range []string{"CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR"} {
				if input, ok := row["credentialFiles"].(map[string]string)["descriptor-3"]; ok && m.Native.Environment[key] != "" {
					path := filepath.Join(m.Native.Temp, "fd-input")
					if err := os.WriteFile(path, []byte(input), 0600); err != nil {
						t.Fatal(err)
					}
					file, err := os.Open(path)
					if err != nil {
						t.Fatal(err)
					}
					defer file.Close()
					m.Native.Environment[key] = strconv.FormatUint(uint64(file.Fd()), 10) + "tail"
				}
			}
			var result cliauth.Result
			err := m.Run(ctx, func(scope *cooperative.Scope) error {
				var err error
				result, err = m.AuthStatus(ctx, scope, false, row["text"] == true)
				return err
			})
			want := expected[i]
			if err != nil || want.Failure != nil || result.Stdout != want.Stdout || result.Stderr != want.Stderr || result.ExitCode != want.ExitCode {
				t.Fatalf("native=%+v err=%v Source=%+v", result, err, want)
			}
			if strings.Contains(result.Stdout, "fake-") {
				t.Fatal("credential in status output")
			}
			for name, want := range expected[i].Persisted {
				data, err := os.ReadFile(filepath.Join(m.credentialDirectory, name))
				if err != nil || string(data) != want {
					t.Fatalf("private CCR side effect %s mismatch: err=%v", name, err)
				}
			}
			// A drained/replaced inherited descriptor is never consulted again.
			m.Native.mu.Lock()
			events := append([]NativeEvent(nil), m.Native.Trace...)
			m.Native.mu.Unlock()
			reads := map[string]int{}
			for _, event := range events {
				if strings.HasPrefix(event.Path, "/dev/fd/") {
					reads[event.Path]++
					if err := os.WriteFile(filepath.Join(m.Native.Temp, "fd-input"), []byte("fake-later-credential"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			var repeated cliauth.Result
			if err := m.Run(ctx, func(scope *cooperative.Scope) error {
				var err error
				repeated, err = m.AuthStatus(ctx, scope, false, row["text"] == true)
				return err
			}); err != nil || repeated.Stdout != result.Stdout || repeated.ExitCode != result.ExitCode {
				t.Fatalf("one-shot command cache changed: %v", err)
			}
			m.Native.mu.Lock()
			after := append([]NativeEvent(nil), m.Native.Trace...)
			m.Native.mu.Unlock()
			for path, before := range reads {
				count := 0
				for _, event := range after {
					if event.Path == path {
						count++
					}
				}
				if count != before {
					t.Fatal("one-shot descriptor was read again", path, before, count)
				}
			}
		})
	}
}
