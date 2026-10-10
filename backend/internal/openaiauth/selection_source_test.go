package openaiauth

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

func TestCodexTransportSelectionMatchesActualSource128Profiles(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	rows := []map[string]bool{}
	inputs := []CodexSelection{}
	for mask := 0; mask < 128; mask++ {
		b := func(i int) bool { return mask&(1<<i) != 0 }
		input := CodexSelection{b(0), b(1), b(2), b(3), b(4), b(5), b(6)}
		inputs = append(inputs, input)
		rows = append(rows, map[string]bool{"hasOpenAIAuth": input.HasOpenAIAuth, "isClaudeSubscriber": input.IsClaudeSubscriber, "forceOpenAICodex": input.ForceOpenAICodex, "isOpenAIModel": input.IsOpenAIModel, "hasAnthropicAuthToken": input.HasAnthropicAuthToken, "hasExplicitApiKey": input.HasExplicitAPIKey, "hasFallbackApiKey": input.HasFallbackAPIKey})
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-openai-transport-selection.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_OPENAI_SELECTION_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(authJSON(rows))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source selection: %v %s", err, stderr.String())
	}
	var expected []bool
	if json.Unmarshal(data, &expected) != nil || len(expected) != 128 {
		t.Fatalf("Source shape: %s", data)
	}
	for i, input := range inputs {
		if got := ShouldUseCodexTransport(input); got != expected[i] {
			t.Errorf("mask %d: Go%v Source%v", i, got, expected[i])
		}
	}
}
