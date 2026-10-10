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

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeCachedModelIdentityUsesActualSourceWithoutInferenceCredentials(t *testing.T) {
	t.Parallel()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	type scenario struct {
		Env         map[string]string `json:"env"`
		Settings    map[string]any    `json:"settings"`
		Credentials map[string]any    `json:"credentials"`
	}
	rows := []scenario{{}, {Env: map[string]string{"ANTHROPIC_BASE_URL": "https://api.deepseek.com/anthropic"}}, {Env: map[string]string{"ANTHROPIC_MODEL": "gpt-6-astra"}}, {Settings: map[string]any{"model": "gpt-6-luna"}}}
	for _, subscription := range []any{nil, "pro", "max", "team", "enterprise"} {
		for _, scopes := range []any{[]string{"user:inference"}, []string{"user:profile"}, "user:inference"} {
			for _, tier := range []string{"default", "default_claude_max_5x"} {
				rows = append(rows, scenario{Credentials: map[string]any{"claudeAiOauth": map[string]any{"accessToken": "fake-only", "refreshToken": "fake-never-refresh", "expiresAt": 0, "scopes": scopes, "subscriptionType": subscription, "rateLimitTier": tier}}})
			}
		}
	}
	max := map[string]any{"claudeAiOauth": map[string]any{"accessToken": "fake-max", "scopes": []string{"user:inference"}, "subscriptionType": "max"}}
	for _, env := range []map[string]string{{"ANTHROPIC_API_KEY": "fake-direct"}, {"ANTHROPIC_AUTH_TOKEN": "fake-external"}, {"ANTHROPIC_AUTH_TOKEN": "fake-managed", "CLAUDE_CODE_ENTRYPOINT": "claude-desktop"}, {"CLAUDE_CODE_SIMPLE": "true"}, {"ANTHROPIC_BASE_URL": "https://api.deepseek.com/anthropic"}, {"CLAUDE_CODE_USE_VERTEX": "1"}, {"CLAUDE_CODE_USE_BEDROCK": "true"}, {"CLAUDE_CODE_USE_FOUNDRY": "yes"}, {"CLAUDE_CODE_USE_AZURE_OPENAI": "on"}, {"CLAUDE_CODE_DISABLE_1M_CONTEXT": "1"}, {"CLAUDE_CODE_OAUTH_TOKEN": "fake-env"}, {"ANTHROPIC_MODEL": "opus[1m]"}, {"ANTHROPIC_MODEL": "haiku"}, {"ANTHROPIC_DEFAULT_OPUS_MODEL": "PrivateOpus"}} {
		rows = append(rows, scenario{Env: env, Credentials: max})
	}
	rows = append(rows, scenario{Credentials: max, Settings: map[string]any{"model": "sonnet"}}, scenario{Credentials: max, Settings: map[string]any{"model": nil}}, scenario{Credentials: max, Settings: map[string]any{"modelOverrides": map[string]string{"claude-opus-5-5": "PrivateOverride"}}})
	openai := map[string]any{"openaiCodexOauth": map[string]any{"accessToken": "fake-only-openai", "refreshToken": "fake-never-refresh", "expiresAt": 0}}
	rows = append(rows, scenario{Credentials: openai}, scenario{Credentials: openai, Env: map[string]string{"CLAUDE_CODE_SIMPLE": "true"}}, scenario{Credentials: openai, Env: map[string]string{"ANTHROPIC_AUTH_TOKEN": "fake-competing"}})
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-model-metadata.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_MODEL_METADATA_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": home, "cases": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual model Source: %v %s", err, stderr.String())
	}
	var original struct {
		Rows []struct {
			Model, DefaultSetting string
			Subscriber            bool
			Type, RateLimitTier   *string
		}
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(data, &original); err != nil || len(original.Rows) != len(rows) || len(original.Capabilities.Blocked) > 0 {
		t.Fatalf("Source shape/escaped %v %s", err, data)
	}
	for index, row := range rows {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			initial := map[string]string{}
			if row.Env["CLAUDE_CODE_SIMPLE"] != "" {
				initial["CLAUDE_CODE_SIMPLE"] = row.Env["CLAUDE_CODE_SIMPLE"]
			}
			m := openAIAuthMainFixtureWithEnv(t, initial)
			var model string
			var defaultSetting json.RawMessage
			var identity modelselection.MetadataIdentity
			err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL"} {
					if err := m.environment.SetProperty(quoteMain(key), nil); err != nil {
						return err
					}
				}
				for key, value := range row.Env {
					if err := m.environment.SetProperty(quoteMain(key), quoteMain(value)); err != nil {
						return err
					}
				}
				if !m.secure.UpdateSourceJSON(context.Background(), rawMain(row.Credentials)).Success {
					return errors.New("private credentials update failed")
				}
				// The Source fixture resets both memoized identities before each
				// profile; startup has already populated this owner's empty cache.
				if err := m.openAIAuth.ClearCache(scope); err != nil {
					return err
				}
				if err := m.openAIAuth.ClearClaudeTokenCache(scope); err != nil {
					return err
				}
				settings := protocol.NewJSONObject()
				if row.Settings != nil {
					_ = settings.UnmarshalJSON(rawMain(row.Settings))
				}
				selector, err := m.nativeModelSelection(context.Background(), scope, modelselection.Options{Environment: m.nativeOpenAIEnvironment(), Settings: settings})
				if err != nil {
					return err
				}
				model, _, err = selectNativeModel(selector)
				if err != nil {
					return err
				}
				defaultSetting = selector.DefaultSetting()
				identity, err = m.openAIAuth.AnthropicModelIdentity(context.Background(), scope, true)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			want := original.Rows[index]
			if model != want.Model || string(defaultSetting) != string(quoteMain(want.DefaultSetting)) || identity.Subscriber != want.Subscriber {
				t.Fatalf("Go %s %s %+v Source %+v", model, defaultSetting, identity, want)
			}
			if want.Type != nil && identity.SubscriptionType != *want.Type || want.RateLimitTier != nil && identity.RateLimitTier != *want.RateLimitTier {
				t.Fatal("cached subscription metadata mismatch", identity, want)
			}
		})
	}
}
