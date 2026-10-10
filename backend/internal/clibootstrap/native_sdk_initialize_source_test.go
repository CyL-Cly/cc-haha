package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestActualSDKInitializeModelAndAccountMetadataAgainstOriginalSourceProfiles(t *testing.T) {
	t.Parallel()
	profiles := []struct {
		name           string
		env            map[string]string
		tokens, global string
		model          string
	}{
		{name: "external-deepseek", env: map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:1", "ANTHROPIC_API_KEY": "fake629"}, model: "deepseek-flash[1m]"},
		{name: "external-capabilities", env: map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:1", "ANTHROPIC_AUTH_TOKEN": "fake629", "ANTHROPIC_DEFAULT_SONNET_MODEL": "private-model629", "ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES": "effort,xhigh_effort,adaptive_thinking"}, model: "private-model629"},
		{name: "first-party-api", env: map[string]string{"ANTHROPIC_API_KEY": "fake629", "NODE_ENV": "test"}, model: "sonnet"},
		{name: "first-party-api-old-sonnet", env: map[string]string{"ANTHROPIC_API_KEY": "fake629", "ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-sonnet-4-6"}, model: "sonnet"},
		{name: "first-party-pro-old-sonnet-extra", env: map[string]string{"ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-sonnet-4-6"}, tokens: `{"claudeAiOauth":{"accessToken":"fake629","scopes":["user:inference"],"subscriptionType":"pro"}}`, global: `{"cachedExtraUsageDisabledReason":"out_of_credits"}`, model: "sonnet"},
		{name: "first-party-max-old-sonnet-extra", env: map[string]string{"ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-sonnet-4-6"}, tokens: `{"claudeAiOauth":{"accessToken":"fake629","scopes":["user:inference"],"subscriptionType":"max","rateLimitTier":"default_claude_max_5x"}}`, global: `{"cachedExtraUsageDisabledReason":null}`, model: "sonnet"},
		{name: "first-party-env-oauth", env: map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "fake-oauth629"}, model: "opus"},
		{name: "first-party-pro", tokens: `{"claudeAiOauth":{"accessToken":"fake629","scopes":["user:inference"],"subscriptionType":"pro","rateLimitTier":"default_claude_pro"}}`, model: "sonnet"},
		{name: "first-party-max", tokens: `{"claudeAiOauth":{"accessToken":"fake629","scopes":["user:inference"],"subscriptionType":"max","rateLimitTier":"default_claude_max_5x"}}`, model: "opus"},
		{name: "first-party-pro-extra", tokens: `{"claudeAiOauth":{"accessToken":"fake629","scopes":["user:inference"],"subscriptionType":"pro"}}`, global: `{"cachedExtraUsageDisabledReason":null}`, model: "opus[1m]"},
		{name: "first-party-enterprise", tokens: `{"claudeAiOauth":{"accessToken":"fake629","scopes":["user:inference"],"subscriptionType":"enterprise"}}`, global: `{"oauthAccount":{"emailAddress":"private@example.invalid","organizationName":"Private Org"}}`, model: "haiku"},
		{name: "first-party-partial-token", tokens: `{"claudeAiOauth":{"accessToken":"fake629","scopes":["user:inference"]}}`, model: "fable"},
		{name: "subscriber-proxy", env: map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:1"}, tokens: `{"claudeAiOauth":{"accessToken":"fake629","scopes":["user:inference"],"subscriptionType":"max"}}`, model: "opus"},
		{name: "openai-saved", tokens: `{"openaiCodexOauth":{"accessToken":"fake629","refreshToken":"fake-refresh629","expiresAt":4102444800000,"email":"private@example.invalid","accountId":"private629"}}`, model: "gpt-6-astra"},
		{name: "openai-competing-api", env: map[string]string{"ANTHROPIC_API_KEY": "fake629"}, tokens: `{"openaiCodexOauth":{"accessToken":"fake629","refreshToken":"fake-refresh629","expiresAt":4102444800000}}`, model: "sonnet"},
		{name: "bedrock", env: map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1", "ANTHROPIC_API_KEY": "fake629"}, model: "sonnet"},
		{name: "vertex", env: map[string]string{"CLAUDE_CODE_USE_VERTEX": "true", "ANTHROPIC_API_KEY": "fake629"}, model: "haiku"},
		{name: "foundry", env: map[string]string{"CLAUDE_CODE_USE_FOUNDRY": "1", "ANTHROPIC_API_KEY": "fake629"}, model: "opus"},
		{name: "azure", env: map[string]string{"CLAUDE_CODE_USE_AZURE_OPENAI": "1", "ANTHROPIC_API_KEY": "fake629", "AZURE_OPENAI_ENDPOINT": "http://127.0.0.1:1"}, model: "gpt-5.4-codex"},
	}
	for _, profile := range profiles {
		t.Run(profile.name, func(t *testing.T) {
			f := newNativePluginFixture(t)
			f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
			physical, e := filepath.EvalSymlinks(f.home)
			if e != nil {
				t.Fatal(e)
			}
			// The real native boundary's per-process paths retain their physical identity.
			oldHome := f.home
			f.home = physical
			f.config = filepath.Join(physical, ".claude")
			f.cwd = filepath.Join(physical, "project")
			f.n.Home = f.home
			f.n.CWD = f.cwd
			f.n.Environment["HOME"] = f.home
			f.n.Environment["CLAUDE_CONFIG_DIR"] = f.config
			f.write(t, filepath.Join(f.home, ".git", "HEAD"), "ref: refs/heads/private629\n")
			known, e := os.ReadFile(filepath.Join(f.config, "plugins", "known_marketplaces.json"))
			if e != nil {
				t.Fatal(e)
			}
			f.write(t, filepath.Join(f.config, "plugins", "known_marketplaces.json"), strings.ReplaceAll(string(known), oldHome, f.home))
			delete(f.n.Environment, "ANTHROPIC_BASE_URL")
			f.n.Environment["NODE_ENV"] = "production"
			f.n.Environment["CLAUDE_CODE_ENTRYPOINT"] = "sdk-cli"
			for key, value := range profile.env {
				f.n.Environment[key] = value
			}
			f.write(t, filepath.Join(f.config, "remote-settings.json"), `{"disableAutoUpdates":true}`)
			if profile.tokens != "" {
				f.write(t, filepath.Join(f.config, ".credentials.json"), profile.tokens)
			}
			f.n.RunSecurity = func(_ context.Context, c securestorage.Command) ([]byte, error) {
				if strings.Contains(strings.Join(c.Args, " "), "-credentials") && profile.tokens != "" {
					return []byte(profile.tokens), nil
				}
				return nil, fmt.Errorf("private Keychain has no managed API key")
			}
			if profile.global != "" {
				// Both runtimes use the original portable global file beneath CLAUDE_CONFIG_DIR.
				f.write(t, filepath.Join(f.config, ".claude.json"), profile.global)
			}
			fixture, _ := filepath.Abs("../../testdata/native-sdk-initialize-metadata.fixture.ts")
			preload, _ := filepath.Abs("../../../preload")
			sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", "--preload", preload, fixture)
			command.Dir = f.home
			command.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_SDK_METADATA_FIXTURE=1", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_ENTRYPOINT=sdk-cli", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
			for key, value := range profile.env {
				command.Env = append(command.Env, key+"="+value)
			}
			command.Stdin = bytes.NewReader(rawMain(map[string]any{"initial": profile.model, "override": profile.model}))
			var sourceDiagnostics bytes.Buffer
			command.Stderr = &sourceDiagnostics
			source, e := command.Output()
			if e != nil {
				t.Fatal("actual original model/account Source", e, sourceDiagnostics.String())
			}
			var expected protocol.JSONObject
			if e = expected.UnmarshalJSON(source); e != nil {
				t.Fatal(e, string(source))
			}
			caps, _ := expected.Object("capabilities")
			var blocked []string
			_ = json.Unmarshal(caps.Get("blocked"), &blocked)
			if len(blocked) > 0 {
				t.Fatal("Source capability escape", blocked)
			}
			var output, diagnostics bytes.Buffer
			disabled := false
			m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: profile.model, PermissionMode: "default", Sources: DefaultState(), MaxTurns: 100400}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: &output, Error: &diagnostics})
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
			if e = m.StartSession(ctx); e != nil {
				t.Fatal(e, diagnostics.String())
			}
			var actual protocol.JSONObject
			e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				var e error
				actual, e = m.nativeSDKInitializeMetadata(scope)
				return e
			})
			if e != nil {
				t.Fatal("actual MainPrint SDK initialize metadata", e, diagnostics.String())
			}
			for _, key := range []string{"models", "account", "commands", "agents", "output_style", "available_output_styles"} {
				var got, want any
				_ = json.Unmarshal(actual.Get(key), &got)
				_ = json.Unmarshal(expected.Get(key), &want)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("%s real Source mismatch\nGo %s\nSource %s", key, actual.Get(key), expected.Get(key))
				}
			}
		})
	}
}
