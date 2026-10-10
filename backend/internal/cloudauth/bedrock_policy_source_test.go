package cloudauth

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

func TestActualSourceBedrockBetaHeaderBodySplitModelCapabilityAndSDKDuplicates(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []map[string]any{}
	for _, model := range []string{"us.anthropic.claude-sonnet-4-20250514-v1:0", "us.anthropic.claude-haiku-4-5-20251001-v1:0", "us.anthropic.claude-3-haiku-20240307-v1:0", "provider-owned[1m]", "us.anthropic.claude-sonnet-4-6:1m"} {
		for _, agentic := range []bool{false, true} {
			rows = append(rows, map[string]any{"model": model, "agentic": agentic, "env": map[string]string{}, "sdk": []string{}})
		}
	}
	rows = append(rows, map[string]any{"model": "provider-owned[1m]", "agentic": true, "env": map[string]string{"ANTHROPIC_DEFAULT_SONNET_MODEL": "provider-owned", "ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES": "interleaved_thinking,thinking", "ANTHROPIC_BETAS": "manual,manual,context-1m-2025-08-07,tool-search-tool-2025-10-19", "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": "1"}, "sdk": []string{"new-sdk", "new-sdk", "manual", "interleaved-thinking-2025-05-14"}}, map[string]any{"model": "us.anthropic.claude-sonnet-4-6[1m]", "agentic": true, "env": map[string]string{"DISABLE_INTERLEAVED_THINKING": "true", "CLAUDE_CODE_DISABLE_1M_CONTEXT": "1"}, "sdk": []string{}})
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-bedrock-policy.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_BEDROCK_POLICY_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "USER_TYPE=external", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1"}
	raw, _ := json.Marshal(map[string]any{"rows": rows})
	cmd.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Rows []struct {
			Canonical      string
			Headers, Extra []string
		}
	}
	if err = json.Unmarshal(out, &source); err != nil {
		t.Fatal(err)
	}
	for index, row := range rows {
		headers, extra := BedrockBetas(row["model"].(string), source.Rows[index].Canonical, row["env"].(map[string]string), row["sdk"].([]string), row["agentic"].(bool))
		expected, _ := json.Marshal(map[string]any{"headers": source.Rows[index].Headers, "extra": source.Rows[index].Extra})
		compareAWS(t, index, map[string]any{"headers": headers, "extra": extra}, expected)
	}
}
