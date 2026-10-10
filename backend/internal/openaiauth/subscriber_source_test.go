package openaiauth

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestClaudeSubscriberPredicateAgainstActualAuthSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	rows := []map[string]any{}
	secureRows := []any{nil, false, []any{1}, map[string]any{}, map[string]any{"claudeAiOauth": false}, map[string]any{"claudeAiOauth": map[string]any{"accessToken": "", "scopes": []string{"user:inference"}}}}
	for _, scopes := range []any{nil, []any{}, []any{"user:profile"}, []any{"user:inference"}, []any{1, "user:inference"}, "user:inference", "prefix-user:inference-tail", false, 1, map[string]any{}} {
		secureRows = append(secureRows, map[string]any{"claudeAiOauth": map[string]any{"accessToken": "fake-claude-token", "scopes": scopes}})
	}
	for _, env := range []map[string]string{{}, {"CLAUDE_CODE_SIMPLE": "true"}, {"CLAUDE_CODE_OAUTH_TOKEN": "fake-env"}, {"ANTHROPIC_AUTH_TOKEN": "fake-external"}, {"ANTHROPIC_AUTH_TOKEN": "fake-external", "CLAUDE_CODE_ENTRYPOINT": "claude-desktop"}, {"ANTHROPIC_API_KEY": "fake-direct"}, {"ANTHROPIC_UNIX_SOCKET": "/private/not-read.sock"}, {"ANTHROPIC_UNIX_SOCKET": "/private/not-read.sock", "CLAUDE_CODE_OAUTH_TOKEN": "fake-env"}, {"CLAUDE_CODE_USE_BEDROCK": "true"}, {"CLAUDE_CODE_USE_VERTEX": "1"}, {"CLAUDE_CODE_USE_FOUNDRY": "yes"}, {"CLAUDE_CODE_USE_AZURE_OPENAI": "on"}} {
		for _, secure := range secureRows {
			rows = append(rows, map[string]any{"env": env, "secure": secure, "preferThirdParty": true})
		}
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-claude-subscriber.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLAUDE_SUBSCRIBER_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(authJSON(rows))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual subscriber Source: %v %s", err, stderr.String())
	}
	var expected []json.RawMessage
	if json.Unmarshal(data, &expected) != nil || len(expected) != len(rows) {
		t.Fatalf("Source shape: %s", data)
	}
	for i, row := range rows {
		env := row["env"].(map[string]string)
		source := "none"
		if env["ANTHROPIC_API_KEY"] != "" {
			source = "ANTHROPIC_API_KEY"
		}
		enabled := AnthropicAuthEnabled(AnthropicAuthInput{Bare: authEnvTruthy(env["CLAUDE_CODE_SIMPLE"]), Environment: env, APIKeySource: source})
		var token *Value
		reads := 0
		if enabled {
			if env["CLAUDE_CODE_OAUTH_TOKEN"] != "" {
				token = ClaudeEnvironmentToken(env["CLAUDE_CODE_OAUTH_TOKEN"])
			} else {
				reads++
				token = ClaudeTokensFromSecure(authJSON(row["secure"]))
			}
		}
		subscriber, e := ClaudeSubscriber(enabled, token)
		result := map[string]any{"enabled": enabled, "subscriber": subscriber, "reads": reads}
		if e != nil {
			result = map[string]any{"error": map[string]any{"name": "TypeError", "message": e.Error()}, "reads": reads}
		}
		var got, want any
		_ = json.Unmarshal(authJSON(result), &got)
		_ = json.Unmarshal(expected[i], &want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("row%d Go%s\nSource%s", i, authJSON(result), expected[i])
		}
	}
}
