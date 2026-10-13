package compaction

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

func TestCompactPromptsAndSummaryFormattingAgainstFrozenSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-compact-prompt.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	type row struct {
		Custom, Arm, Direction, Summary, Path string
		Suppress, Recent                      bool
	}
	rows := []row{{Summary: "<analysis>scratch</analysis>\n<summary>必须保留模型参数</summary>", Suppress: true, Path: "/private/tmp/private transcript"}, {Arm: "control", Custom: "keep tool result pairs", Summary: "<summary>a\n\n\n b</summary>", Recent: true}, {Direction: "from", Custom: "用户明确约束", Summary: "<analysis>a</analysis><analysis>b</analysis><summary>x</summary>"}, {Direction: "up_to", Summary: "raw text"}}
	inputRows := []map[string]any{}
	for _, r := range rows {
		inputRows = append(inputRows, map[string]any{"custom": r.Custom, "arm": r.Arm, "direction": r.Direction, "summary": r.Summary, "path": r.Path, "suppress": r.Suppress, "recent": r.Recent})
	}
	raw, _ := json.Marshal(map[string]any{"homeDir": home, "rows": inputRows})
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_COMPACT_PROMPT_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw)
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("actual Source prompt %v %.3000s", e, output)
	}
	var source struct {
		Rows []struct{ Prompt, Formatted, User string }
	}
	if e = json.Unmarshal(output, &source); e != nil || len(source.Rows) != len(rows) {
		t.Fatal(e, string(output))
	}
	for i, r := range rows {
		if got := Prompt(r.Custom, r.Arm, r.Direction); got != source.Rows[i].Prompt {
			t.Errorf("prompt row%d Go%s Source%s", i, got, source.Rows[i].Prompt)
		}
		if got := FormatSummary(r.Summary); got != source.Rows[i].Formatted {
			t.Error(i, "format", got, source.Rows[i].Formatted)
		}
		if got := UserSummary(r.Summary, r.Suppress, r.Path, r.Recent); got != source.Rows[i].User {
			t.Error(i, "continuation", got, source.Rows[i].User)
		}
	}
}
