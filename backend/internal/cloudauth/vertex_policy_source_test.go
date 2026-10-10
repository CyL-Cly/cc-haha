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

func TestActualSourceVertexBetaHeadersRegionModelCapabilityAndSDKDuplicates(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []map[string]any{}
	for _, model := range []string{"claude-sonnet-4@20250514", "claude-haiku-4-5@20251001", "claude-3-haiku@20240307", "provider-owned[1m]", "claude-sonnet-4-6:1m"} {
		for _, agentic := range []bool{true} {
			rows = append(rows, map[string]any{"model": model, "agentic": agentic, "env": map[string]string{}, "sdk": []string{}})
		}
	}
	rows = append(rows, map[string]any{"model": "provider-owned[1m]", "agentic": true, "env": map[string]string{"ANTHROPIC_DEFAULT_SONNET_MODEL": "provider-owned", "ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES": "interleaved_thinking,thinking", "ANTHROPIC_BETAS": "manual,manual,context-1m-2025-08-07,tool-search-tool-2025-10-19", "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": "1"}, "sdk": []string{"new-sdk", "new-sdk", "manual", "interleaved-thinking-2025-05-14"}}, map[string]any{"model": "claude-sonnet-4-6[1m]", "agentic": true, "env": map[string]string{"DISABLE_INTERLEAVED_THINKING": "true", "CLAUDE_CODE_DISABLE_1M_CONTEXT": "1"}, "sdk": []string{}})
	for _, model := range []string{"claude-haiku-4-5@20251001", "claude-3-5-haiku@20241022", "claude-3-5-sonnet@20241022", "claude-3-7-sonnet@20250219", "claude-opus-4-1@20250805", "claude-opus-4@20250514", "claude-sonnet-4-6@20260217", "claude-sonnet-4-5@20250929", "claude-sonnet-4@20250514", "UNKNOWN", ""} {
		rows = append(rows, map[string]any{"model": model, "agentic": true, "env": map[string]string{"CLOUD_ML_REGION": "default-private", "VERTEX_REGION_CLAUDE_HAIKU_4_5": "haiku45", "VERTEX_REGION_CLAUDE_3_5_HAIKU": "haiku35", "VERTEX_REGION_CLAUDE_3_5_SONNET": "sonnet35", "VERTEX_REGION_CLAUDE_3_7_SONNET": "sonnet37", "VERTEX_REGION_CLAUDE_4_1_OPUS": "opus41", "VERTEX_REGION_CLAUDE_4_0_OPUS": "opus4", "VERTEX_REGION_CLAUDE_4_6_SONNET": "sonnet46", "VERTEX_REGION_CLAUDE_4_5_SONNET": "sonnet45", "VERTEX_REGION_CLAUDE_4_0_SONNET": "sonnet4"}, "sdk": []string{}})
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-vertex-policy.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_VERTEX_POLICY_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "USER_TYPE=external", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1"}
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
			Canonical string
			Headers   []string
			Region    string
		}
	}
	if err = json.Unmarshal(out, &source); err != nil {
		t.Fatal(err)
	}
	for index, row := range rows {
		headers := VertexBetas(row["model"].(string), source.Rows[index].Canonical, row["env"].(map[string]string), row["sdk"].([]string))
		region := VertexRegion(row["model"].(string), row["env"].(map[string]string))
		expected, _ := json.Marshal(map[string]any{"headers": source.Rows[index].Headers, "region": source.Rows[index].Region})
		compareAWS(t, index, map[string]any{"headers": headers, "region": region}, expected)
	}
}
