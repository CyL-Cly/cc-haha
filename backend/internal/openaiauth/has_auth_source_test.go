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

func TestHasOpenAIAuthLegacyTruthinessMatchesActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	rows := []any{nil, false, true, 0, 1, "", "text", []any{1}, map[string]any{}, map[string]any{"accessToken": "fake-only-access"}}
	for _, refresh := range []any{nil, false, true, 0, 1, -1, "", "fake-refresh", "\x00", []any{}, []any{nil}, map[string]any{}, json.RawMessage("-0"), json.RawMessage("1e999"), json.RawMessage(`"\ud800"`)} {
		rows = append(rows, map[string]any{"refreshToken": refresh})
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-openai-has-auth.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_OPENAI_HAS_AUTH_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(authJSON(rows))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source auth presence: %v %s", err, stderr.String())
	}
	var expected []bool
	if json.Unmarshal(data, &expected) != nil || len(expected) != len(rows) {
		t.Fatalf("Source shape: %s", data)
	}
	for i, row := range rows {
		value, err := NewValue(authJSON(row))
		if err != nil {
			t.Fatal(err)
		}
		if got := HasOpenAIAuth(value); got != expected[i] {
			t.Errorf("row%d Go%v Source%v", i, got, expected[i])
		}
	}
}
