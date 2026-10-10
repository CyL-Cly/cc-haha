package mcpcli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestClaudeAIMCPEligibilityMatchesActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	rows := []map[string]any{}
	for _, secure := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`true`), json.RawMessage(`[]`), json.RawMessage(`{"claudeAiOauth":null}`), json.RawMessage(`{"claudeAiOauth":{"scopes":["user:mcp_servers"]}}`), json.RawMessage(`{"claudeAiOauth":{"accessToken":"fake-access"}}`), json.RawMessage(`{"claudeAiOauth":{"accessToken":"fake-access","scopes":null}}`), json.RawMessage(`{"claudeAiOauth":{"accessToken":"fake-access","scopes":1}}`), json.RawMessage(`{"claudeAiOauth":{"accessToken":"fake-access","scopes":{}}}`), json.RawMessage(`{"claudeAiOauth":{"accessToken":"fake-access","scopes":[]}}`), json.RawMessage(`{"claudeAiOauth":{"accessToken":"fake-access","scopes":["user:inference","user:mcp_servers"]}}`), json.RawMessage(`{"claudeAiOauth":{"accessToken":"fake-access","scopes":["u\u0073er:mcp_servers"]}}`), json.RawMessage(`{"claudeAiOauth":{"accessToken":"fake-access","scopes":"prefix-user:mcp_servers-suffix"}}`)} {
		rows = append(rows, map[string]any{"secure": secure})
	}
	eligible := json.RawMessage(`{"claudeAiOauth":{"accessToken":"fake-access","scopes":["user:mcp_servers"]}}`)
	for _, environment := range []map[string]string{{"ANTHROPIC_API_KEY": "fake-api-key"}, {"ANTHROPIC_AUTH_TOKEN": "fake-external"}, {"CLAUDE_CODE_OAUTH_TOKEN": "forced-inference"}, {"ENABLE_CLAUDEAI_MCP_SERVERS": "0"}, {"ENABLE_CLAUDEAI_MCP_SERVERS": " FaLsE "}, {"ENABLE_CLAUDEAI_MCP_SERVERS": "OFF"}, {"ENABLE_CLAUDEAI_MCP_SERVERS": ""}, {"ENABLE_CLAUDEAI_MCP_SERVERS": "1"}} {
		rows = append(rows, map[string]any{"secure": eligible, "environment": environment})
	}
	rows = append(rows, map[string]any{"secure": eligible, "bare": true})
	rows = append(rows, map[string]any{"secure": eligible, "environment": map[string]string{"CLAUDE_CODE_SIMPLE": "1"}}, map[string]any{"secure": eligible, "environment": map[string]string{"CLAUDE_CODE_SIMPLE": " YeS "}})
	input, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cli-mcp-eligibility.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLI_MCP_ELIGIBILITY_FIXTURE=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	result, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var expected []bool
	if err = json.Unmarshal(result, &expected); err != nil || len(expected) != len(rows) {
		t.Fatal(err, string(result))
	}
	for i, row := range rows {
		env, _ := row["environment"].(map[string]string)
		secure, _ := row["secure"].(json.RawMessage)
		got, err := ClaudeAIEligibility(env, row["bare"] == true, secure)
		if err != nil || got != expected[i] {
			t.Errorf("case%d native(%v,%v) Source%v", i, got, err, expected[i])
		}
	}
}
