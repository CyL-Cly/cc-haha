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
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func authCommandFixture(t *testing.T, row map[string]any) *MainCommand {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	home, cwd, tmp := filepath.Join(root, "home"), filepath.Join(root, "workspace"), filepath.Join(root, "tmp")
	for _, p := range []string{home, cwd, tmp} {
		if e = os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude"), "TMPDIR": tmp, "USER_TYPE": "external", "NODE_ENV": "production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH": "1"}
	for k, v := range row["env"].(map[string]string) {
		env[k] = v
	}
	n, e := NewProcessNativeBoundary(NativeProcessOptions{Environment: env, CWD: cwd, Platform: "darwin", ManagedRoot: filepath.Join(root, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if e != nil {
		t.Fatal(e)
	}
	n.Temp = tmp
	n.RunSecurity = func(ctx context.Context, command securestorage.Command) ([]byte, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if strings.Contains(strings.Join(command.Args, " "), "-credentials") {
			return rawMain(row["secure"]), nil
		}
		if legacy, ok := row["legacy"].(string); ok {
			return []byte(legacy), nil
		}
		return nil, errors.New("private keychain absent")
	}
	if settings, ok := row["settings"]; ok {
		agentsWrite(t, filepath.Join(env["CLAUDE_CONFIG_DIR"], "settings.json"), string(rawMain(settings)))
	}
	if global, ok := row["global"]; ok {
		agentsWrite(t, filepath.Join(env["CLAUDE_CONFIG_DIR"], ".claude.json"), string(rawMain(global)))
	}
	args := []string{"auth", "status"}
	if row["text"] == true {
		args = append(args, "--text")
	}
	if row["openai"] == true {
		args = append(args, "--openai")
	}
	if flag, ok := row["flagSettings"]; ok {
		args = append([]string{"--settings", string(rawMain(flag))}, args...)
	}
	m, e := NewMainCommand(MainCommandOptions{Arguments: args, Native: n, Platform: "darwin", Version: "test", PID: os.Getpid(), NonInteractive: row["nonInteractive"] == true, Output: new(bytes.Buffer), Error: new(bytes.Buffer)})
	if e != nil {
		t.Fatal(e)
	}
	m.credentialDirectory = filepath.Join(tmp, "remote")
	credentialFiles, _ := row["credentialFiles"].(map[string]string)
	for filename, data := range credentialFiles {
		if filename == ".oauth_token" || filename == ".api_key" {
			agentsWrite(t, filepath.Join(m.credentialDirectory, filename), data)
		}
	}
	t.Cleanup(m.Close)
	return m
}

func TestCommandAuthStatusActualSourceAndSameNativeCredentials(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	rows := []map[string]any{}
	account := map[string]any{"emailAddress": "private@example.invalid", "organizationUuid": "org-private", "organizationName": "Private", "future": true}
	claude := map[string]any{"accessToken": "fake-claude", "refreshToken": "fake-refresh", "expiresAt": 17, "scopes": []string{"user:inference"}, "subscriptionType": "max"}
	openai := map[string]any{"accessToken": "fake-openai", "refreshToken": "fake-openai-refresh", "expiresAt": 17, "email": "openai@example.invalid", "accountId": "fake-account"}
	for _, profile := range []map[string]any{
		{"env": map[string]string{}},
		{"env": map[string]string{"ANTHROPIC_AUTH_TOKEN": "fake-token", "ANTHROPIC_BASE_URL": "https://api.deepseek.com/anthropic"}},
		{"env": map[string]string{"ANTHROPIC_API_KEY": "fake-api"}, "nonInteractive": true},
		{"env": map[string]string{"ANTHROPIC_API_KEY": "fake-unapproved"}},
		{"env": map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "fake-oauth"}},
		{"env": map[string]string{}, "secure": map[string]any{"claudeAiOauth": claude}, "global": map[string]any{"oauthAccount": account}},
		{"env": map[string]string{}, "secure": map[string]any{"openaiCodexOauth": openai}},
		{"env": map[string]string{}, "secure": map[string]any{"openaiCodexOauth": openai, "claudeAiOauth": claude}, "global": map[string]any{"oauthAccount": account}},
		{"env": map[string]string{}, "global": map[string]any{"primaryApiKey": "fake-managed", "oauthAccount": account}, "secure": map[string]any{"claudeAiOauth": claude}},
		{"env": map[string]string{}, "legacy": "fake-legacy"},
		{"env": map[string]string{}, "settings": map[string]any{"apiKeyHelper": "never-execute-private-command"}},
		{"env": map[string]string{"CLAUDE_CODE_SIMPLE": "1"}, "secure": map[string]any{"claudeAiOauth": claude, "openaiCodexOauth": openai}},
		{"env": map[string]string{"CLAUDE_CODE_SIMPLE": "1"}, "flagSettings": map[string]any{"apiKeyHelper": "never-execute-bare-command"}},
		{"env": map[string]string{"IS_DEMO": "1"}, "secure": map[string]any{"claudeAiOauth": claude}, "global": map[string]any{"oauthAccount": account}},
		{"env": map[string]string{"CLAUDE_CODE_USE_BEDROCK": "yes", "AWS_DEFAULT_REGION": "eu-west-1", "CLAUDE_CODE_SKIP_BEDROCK_AUTH": "1", "BEDROCK_BASE_URL": "https://bedrock.invalid", "https_proxy": "http://private.invalid:1", "HTTPS_PROXY": "http://unused.invalid:2"}},
		{"env": map[string]string{"CLAUDE_CODE_USE_VERTEX": "TRUE", "ANTHROPIC_VERTEX_PROJECT_ID": "fake-project", "CLOUD_ML_REGION": "eu-west4", "CLAUDE_CODE_SKIP_VERTEX_AUTH": "true"}},
		{"env": map[string]string{"CLAUDE_CODE_USE_FOUNDRY": "1", "ANTHROPIC_FOUNDRY_RESOURCE": "fake-resource", "ANTHROPIC_FOUNDRY_BASE_URL": "https://foundry.invalid", "CLAUDE_CODE_SKIP_FOUNDRY_AUTH": "true"}},
		{"env": map[string]string{"CLAUDE_CODE_USE_AZURE_OPENAI": "1"}},
		{"env": map[string]string{}, "secure": map[string]any{"openaiCodexOauth": openai}, "openai": true},
		{"env": map[string]string{}, "openai": true},
	} {
		for _, text := range []bool{false, true} {
			copy := map[string]any{}
			for k, v := range profile {
				copy[k] = v
			}
			copy["text"] = text
			rows = append(rows, copy)
		}
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-command-auth-status.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_COMMAND_AUTH_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(rows))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source: %v %s", e, stderr.String())
	}
	var expected []struct {
		Stdout, Stderr string
		ExitCode       int
		Failure        any
	}
	if json.Unmarshal(data, &expected) != nil || len(expected) != len(rows) {
		t.Fatalf("Source shape %s", data)
	}
	for i, row := range rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			m := authCommandFixture(t, row)
			var result cliauth.Result
			e := m.Run(context.Background(), func(scope *cooperative.Scope) error {
				var err error
				result, err = m.AuthStatus(context.Background(), scope, row["openai"] == true, row["text"] == true)
				return err
			})
			want := expected[i]
			if e != nil || want.Failure != nil || result.Stdout != want.Stdout || result.Stderr != want.Stderr || result.ExitCode != want.ExitCode {
				t.Errorf("native=%+v err=%v Source=%+v", result, e, want)
			}
			if strings.Contains(result.Stdout, "fake-token") || strings.Contains(result.Stdout, "fake-openai-refresh") {
				t.Fatal("credential in output")
			}
		})
	}
}
