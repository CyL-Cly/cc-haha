package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestSetupTokenWarningActualSourceOnlyReadsAuthenticationSources(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source unavailable")
	}
	fixture, _ := filepath.Abs("../../testdata/native-cli-setup-token.fixture.ts")
	rows := []map[string]any{
		{"env": map[string]string{}},
		{"env": map[string]string{"CLAUDE_CODE_SIMPLE": "1"}},
		{"env": map[string]string{"ANTHROPIC_UNIX_SOCKET": "/owned/missing"}},
		{"env": map[string]string{"ANTHROPIC_UNIX_SOCKET": "/owned/missing", "CLAUDE_CODE_OAUTH_TOKEN": "FAKE_PLACEHOLDER"}, "settings": map[string]any{"apiKeyHelper": "never-run"}},
		{"env": map[string]string{"ANTHROPIC_AUTH_TOKEN": "FAKE_EXTERNAL"}},
		{"env": map[string]string{"ANTHROPIC_AUTH_TOKEN": "FAKE_EXTERNAL", "CLAUDE_CODE_REMOTE": "true"}},
		{"env": map[string]string{}, "settings": map[string]any{"apiKeyHelper": "never-run"}},
		{"env": map[string]string{"CLAUDE_CODE_ENTRYPOINT": "claude-desktop"}, "settings": map[string]any{"apiKeyHelper": "never-run"}},
		{"env": map[string]string{"ANTHROPIC_API_KEY": "FAKE_UNAPPROVED"}},
		{"env": map[string]string{"ANTHROPIC_API_KEY": "FAKE_APPROVED"}, "global": map[string]any{"customApiKeyResponses": map[string]any{"approved": []string{"FAKE_APPROVED"}}}},
		{"env": map[string]string{"CI": "true", "ANTHROPIC_API_KEY": "FAKE_CI"}},
		{"env": map[string]string{"NODE_ENV": "test", "ANTHROPIC_API_KEY": "FAKE_TEST"}},
		{"env": map[string]string{"CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR": "3"}, "credentialFiles": map[string]string{".api_key": "FAKE_PRIVATE_FD"}},
	}
	for _, key := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		for _, value := range []string{"false", " yes "} {
			rows = append(rows, map[string]any{"env": map[string]string{key: value}})
		}
	}
	for i, row := range rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			m := authCommandFixture(t, row)
			settings, e := m.Startup.GetInitialSettings()
			if e != nil {
				t.Fatal(e)
			}
			var got bool
			e = m.Run(context.Background(), func(scope *cooperative.Scope) error {
				var e error
				got, e = m.setupTokenAuthWarning(context.Background(), settings.Clone())
				return e
			})
			if e != nil {
				t.Fatal(e)
			}
			root := t.TempDir()
			home, e := filepath.EvalSymlinks(filepath.Join(root, "home"))
			if os.IsNotExist(e) {
				os.Mkdir(filepath.Join(root, "home"), 0700)
				home, _ = filepath.EvalSymlinks(filepath.Join(root, "home"))
			}
			config := filepath.Join(home, ".claude")
			os.Mkdir(config, 0700)
			if value, ok := row["settings"]; ok {
				os.WriteFile(filepath.Join(config, "settings.json"), rawMain(value), 0600)
			}
			if value, ok := row["global"]; ok {
				os.WriteFile(filepath.Join(config, ".claude.json"), rawMain(value), 0600)
			}
			command := exec.Command(bun, "--no-env-file", fixture)
			command.Dir = home
			env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": config, "TMPDIR": home, "CC_HAHA_FIXTURE_TEMP_ROOT": root, "CC_HAHA_CLI_LOGIN_FIXTURE": "1", "CC_HAHA_CLI_LOGIN_PEER": "https://127.0.0.1:1", "CC_HAHA_SETUP_WARNING_ONLY": "1", "BUN_OPTIONS": "--no-env-file", "PATH": "/usr/bin:/bin", "DISABLE_TELEMETRY": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH": "1", "USER_TYPE": "external", "NODE_ENV": "production"}
			for k, v := range row["env"].(map[string]string) {
				env[k] = v
			}
			if env["CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR"] != "" {
				path := filepath.Join(home, "fd-key")
				os.WriteFile(path, []byte("FAKE_PRIVATE_FD"), 0600)
				file, e := os.Open(path)
				if e != nil {
					t.Fatal(e)
				}
				defer file.Close()
				command.ExtraFiles = []*os.File{file}
			}
			for k, v := range env {
				command.Env = append(command.Env, k+"="+v)
			}
			var stderr bytes.Buffer
			command.Stderr = &stderr
			data, e := command.Output()
			if e != nil {
				t.Fatal(e, stderr.String(), string(data))
			}
			var want struct{ Warning bool }
			if json.Unmarshal(data, &want) != nil {
				t.Fatal(string(data))
			}
			if got != want.Warning {
				t.Fatalf("actual warning source=%v go=%v row=%v", want.Warning, got, i)
			}
			if strings.Contains(stderr.String(), "never-run") {
				t.Fatal("auth warning executed helper")
			}
		})
	}
}
