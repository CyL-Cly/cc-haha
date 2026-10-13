package querypreflight

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestActualSourceSDKBetasFeedConfiguredWindowAndAutoCompactThreshold(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	rows := []map[string]any{}
	for _, model := range []string{"claude-sonnet-4-6", "claude-sonnet-4-6-private", "claude-opus-4-6-private", "unknown-family", "claude-sonnet-4-6-private[1m]"} {
		for _, env := range []map[string]string{{}, {"CLAUDE_CODE_DISABLE_1M_CONTEXT": "1"}, {"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "64000"}, {"CLAUDE_CODE_MODEL_CONTEXT_WINDOWS": `{"claude-sonnet-4-6-private":128000}`}} {
			rows = append(rows, map[string]any{"name": model, "model": model, "sdkBetas": []string{"context-1m-2025-08-07", "context-1m-2025-08-07"}, "env": env})
		}
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-preflight.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_PREFLIGHT_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "ANTHROPIC_API_KEY=fake-only-private", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "scenarios": rows})
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var source struct {
		Observations []struct {
			Observation struct{ Effective, Threshold float64 }
		}
		SourceOnly struct{ Capabilities struct{ Blocked []string } }
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Observations) != len(rows) || len(source.SourceOnly.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(out))
	}
	for i, row := range rows {
		model := row["model"].(string)
		env := row["env"].(map[string]string)
		window, err := (modelaccounting.RestoredModelResolver{Environment: env, SDKBetas: row["sdkBetas"].([]string)}).ContextWindow(model)
		if err != nil {
			t.Fatal(err)
		}
		_, known := nativeContextWindow(model, env)
		effective, threshold := nativeThresholdsForWindow(model, env, window, known)
		if effective != source.Observations[i].Observation.Effective || threshold != source.Observations[i].Observation.Threshold {
			t.Error(i, model, window, effective, threshold, source.Observations[i].Observation)
		}
	}
}
