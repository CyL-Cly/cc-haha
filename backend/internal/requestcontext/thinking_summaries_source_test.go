package requestcontext

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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeThinkingSummaryBetaAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []map[string]any{}
	for _, env := range []map[string]string{{}, {"ANTHROPIC_BASE_URL": "https://api.deepseek.com/anthropic"}, {"ANTHROPIC_BASE_URL": "https://api.anthropic.com:443"}, {"ANTHROPIC_BASE_URL": "https://api.anthropic.com:8443"}, {"CLAUDE_CODE_USE_BEDROCK": "1"}, {"CLAUDE_CODE_USE_VERTEX": "1"}, {"CLAUDE_CODE_USE_FOUNDRY": "1"}, {"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": "1"}, {"DISABLE_INTERLEAVED_THINKING": "1"}, {"ANTHROPIC_BASE_URL": "https://api.deepseek.com/anthropic", "ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-sonnet-4-6", "ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES": "interleaved_thinking"}, {"ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-sonnet-4-6", "ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES": ""}} {
		for _, interactive := range []bool{false, true} {
			for _, settings := range []map[string]any{{}, {"showThinkingSummaries": false}, {"showThinkingSummaries": true}} {
				for _, model := range []string{"claude-sonnet-4-6", "claude-3-5-sonnet", "claude-haiku-4-5"} {
					rows = append(rows, map[string]any{"env": env, "interactive": interactive, "settings": settings, "model": model})
					if env["CLAUDE_CODE_USE_BEDROCK"] == "" && env["CLAUDE_CODE_USE_VERTEX"] == "" && env["CLAUDE_CODE_USE_FOUNDRY"] == "" {
						rows = append(rows, map[string]any{"env": env, "interactive": interactive, "settings": settings, "model": model, "count": true})
					}
				}
			}
		}
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-thinking-summaries.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_THINKING_SUMMARIES_FIXTURE=1", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var expected []struct {
		Redacted bool
		Values   []string
	}
	if err := json.Unmarshal(out, &expected); err != nil || len(expected) != len(rows) {
		t.Fatal(err, string(out))
	}
	for i, row := range rows {
		encoded, _ := json.Marshal(row["settings"])
		var settings protocol.JSONObject
		_ = settings.UnmarshalJSON(encoded)
		if row["count"] == true {
			values, err := DirectCountingBetas(row["model"].(string), row["env"].(map[string]string), settings)
			if err != nil {
				t.Fatal(err)
			}
			values = WithThinkingSummaryBeta(values, NativeThinkingSummaryBeta(row["model"].(string), row["env"].(map[string]string), row["interactive"].(bool), settings))
			if !reflect.DeepEqual(values, expected[i].Values) {
				t.Error(i, row, values, expected[i].Values)
			}
		}
		if got := NativeThinkingSummaryBeta(row["model"].(string), row["env"].(map[string]string), row["interactive"].(bool), settings); got != expected[i].Redacted {
			t.Error(i, row, got, expected[i].Redacted)
		}
	}
}
