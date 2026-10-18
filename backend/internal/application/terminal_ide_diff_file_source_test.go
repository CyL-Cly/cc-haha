package application

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTerminalIDEDiffOriginalFullFileToolPhysicalSaveStalenessAndUpdatedInput(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-ide-file-consumer.fixture.ts")
	rows := []any{}
	for _, tool := range []string{"Edit", "Write"} {
		for _, physical := range []bool{false, true} {
			rows = append(rows, map[string]any{"name": tool + map[bool]string{false: "-virtual", true: "-physical"}[physical], "tool": tool, "physicalSave": physical, "old": "head\nold\nlast\n", "proposal": "head\nnew\nlast\n", "saved": "head\nUSER_IDE_PHYSICAL_SAVE_526\nlast\n"})
		}
	}
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_IDE_FILE_SOURCE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ANTHROPIC_API_KEY=private-source-no-inference", "ANTHROPIC_BASE_URL=http://127.0.0.1:1"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "rows": rows}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("original full file consumer %v %.8000s", err, output)
	}
	var result struct {
		Results []struct {
			Bytes        string
			Error        *string
			UpdatedInput json.RawMessage
		}
		Capabilities struct{ Blocked, SecureReads []string }
	}
	if err = json.Unmarshal(output, &result); err != nil {
		t.Fatal(err, string(output))
	}
	if len(result.Results) != 4 || len(result.Capabilities.Blocked) != 0 || len(result.Capabilities.SecureReads) != 0 {
		t.Fatal("original private full consumer missing", string(output))
	}
	for index, observation := range result.Results {
		physical := index%2 == 1
		want := "head\nUSER_IDE_PHYSICAL_SAVE_526\nlast\n"
		if index == 2 {
			want = strings.TrimSuffix(want, "\n")
		}
		if observation.Bytes != want {
			t.Fatal("original file bytes differ from same Go consumer proof", index, observation.Bytes, want)
		}
		if physical {
			if observation.Error == nil || !strings.Contains(*observation.Error, "unexpectedly modified") {
				t.Fatal("actual original tool did not retain stale-save guard", index, observation.Error)
			}
		} else if observation.Error != nil {
			t.Fatal("original updated input failed without physical save", index, *observation.Error)
		}
	}
}
