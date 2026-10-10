package teamspawn

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestActualTeammateModelHardFallbackAndMappedOpusBeforeProviderAgainstSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	rows := []map[string]string{{}, {"ANTHROPIC_DEFAULT_OPUS_MODEL": " deepseek-flash[1m] "}, {"ANTHROPIC_DEFAULT_OPUS_MODEL": "\u00a0 ", "CLAUDE_CODE_USE_VERTEX": "true"}, {"CLAUDE_CODE_USE_BEDROCK": "1"}, {"CLAUDE_CODE_USE_VERTEX": "TRUE"}, {"CLAUDE_CODE_USE_FOUNDRY": "yes"}, {"CLAUDE_CODE_USE_AZURE_OPENAI": "on"}, {"CLAUDE_CODE_USE_BEDROCK": "false", "CLAUDE_CODE_USE_VERTEX": "1"}, {"CLAUDE_CODE_USE_BEDROCK": "true", "CLAUDE_CODE_USE_VERTEX": "true", "CLAUDE_CODE_USE_FOUNDRY": "true"}, {"CLAUDE_CODE_USE_VERTEX": "\u00a0 TRUE \n"}}
	actual := []string{}
	for _, row := range rows {
		actual = append(actual, HardcodedModelFallback(row))
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-model-fallback.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_FALLBACK_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawJSON(map[string]any{"homeDir": home, "rows": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(output))
	}
	var source struct{ Rows []string }
	if err = json.Unmarshal(output, &source); err != nil || !reflect.DeepEqual(actual, source.Rows) {
		t.Fatal(err, actual, string(output))
	}
}
