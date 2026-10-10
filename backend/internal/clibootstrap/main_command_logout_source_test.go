package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestCommandLogoutActualSourceStateTransitionsAndPrivateFiles(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	global := map[string]any{"primaryApiKey": "fake-global", "oauthAccount": map[string]any{"emailAddress": "private@example.invalid"}, "hasCompletedOnboarding": true, "futureRoot": "keep"}
	secure := map[string]any{"openaiCodexOauth": map[string]any{"accessToken": "fake-openai", "refreshToken": "fake-refresh", "expiresAt": 17}, "claudeAiOauth": map[string]any{"accessToken": "fake-claude", "refreshToken": "fake-claude-refresh"}, "grokOauth": map[string]any{"future": true}, "futureSecure": "preserve"}
	rows := []map[string]any{}
	for _, openAI := range []bool{false, true} {
		for _, fileBacked := range []bool{false, true} {
			rows = append(rows, map[string]any{"openai": openAI, "fileBacked": fileBacked, "global": global, "secure": secure, "token": map[string]any{"accessToken": "fake-file", "refreshToken": "fake-file-refresh", "expiresAt": 17}})
		}
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-command-auth-logout.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_COMMAND_LOGOUT_FIXTURE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(rows))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source logout: %v %s", err, stderr.String())
	}
	var expected []struct {
		Stdout, Stderr        string
		ExitCode              int
		Secure, Global        protocol.JSONObject
		Marker, Token, Remote *string
		Failure               any
		Calls                 []string
	}
	if json.Unmarshal(data, &expected) != nil || len(expected) != len(rows) {
		t.Fatalf("Source shape: %s", data)
	}
	for i, row := range rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			m := authCommandFixture(t, map[string]any{"env": map[string]string{}, "global": global})
			config := m.Native.Environment["CLAUDE_CONFIG_DIR"]
			credential := filepath.Join(config, ".credentials.json")
			remote := filepath.Join(config, "remote-settings.json")
			token := filepath.Join(config, "selected-openai.json")
			agentsWrite(t, credential, string(rawMain(secure)))
			agentsWrite(t, remote, "{}")
			isolated, err := securestorage.New(securestorage.Options{Home: m.Native.Home, ConfigDir: config, ConfigDirExplicit: true, Platform: "darwin", Environment: m.Native.Environment, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) {
				return nil, errors.New("isolated unavailable")
			}})
			if err != nil {
				t.Fatal(err)
			}
			m.Secure = isolated
			if row["fileBacked"] == true {
				m.Native.Environment["OPENAI_CODEX_OAUTH_FILE"] = token
				agentsWrite(t, token, string(rawMain(row["token"])))
			}
			var actual cliauth.Result
			err = m.Run(context.Background(), func(scope *cooperative.Scope) error {
				var err error
				actual, err = m.AuthLogout(context.Background(), scope, row["openai"] == true)
				return err
			})
			want := expected[i]
			if err != nil || want.Failure != nil || actual.Stdout != want.Stdout || actual.Stderr != want.Stderr || actual.ExitCode != want.ExitCode {
				t.Fatalf("Go%+v err%v Source%+v", actual, err, want)
			}
			state, err := m.Global.Get()
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"primaryApiKey", "oauthAccount", "hasCompletedOnboarding", "futureRoot"} {
				if string(state.Get(key)) != string(want.Global.Get(key)) {
					t.Error("global transition", key, string(state.Get(key)), string(want.Global.Get(key)))
				}
			}
			readOptional := func(path string) *string {
				data, err := os.ReadFile(path)
				if os.IsNotExist(err) {
					return nil
				}
				if err != nil {
					t.Fatal(err)
				}
				value := string(data)
				return &value
			}
			same := func(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
			if !same(readOptional(filepath.Join(config, "cc-haha/openai-oauth-file-backed")), want.Marker) || !same(readOptional(remote), want.Remote) || !same(readOptional(token), want.Token) {
				t.Fatal("file/marker/cache transition differs from Source", want)
			}
			actualSecure, err := os.ReadFile(credential)
			if want.Secure.IsObject() {
				if err != nil {
					t.Fatal(err)
				}
				var value protocol.JSONObject
				if err = value.UnmarshalJSON(actualSecure); err != nil {
					t.Fatal(err)
				}
				for _, key := range []string{"claudeAiOauth", "openaiCodexOauth", "grokOauth", "futureSecure"} {
					a, _ := protocol.ParseAndStringifyJavaScriptJSON(value.Get(key))
					b, _ := protocol.ParseAndStringifyJavaScriptJSON(want.Secure.Get(key))
					if !bytes.Equal(a, b) {
						t.Error("secure transition", key, string(a), string(b))
					}
				}
			} else if !os.IsNotExist(err) {
				t.Fatal("general logout retained physical credential record", err)
			}
		})
	}
}
