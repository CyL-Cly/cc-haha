package sdkbetas

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
)

func TestActualSourceCLIBetaAllowlistDuplicatesWhitespaceAndSubscriberWarnings(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []map[string]any{}
	for _, values := range [][]string{nil, {}, {Context1M}, {"invalid"}, {"invalid", Context1M, Context1M, " context-1m-2025-08-07", "\ufeff", "未来"}} {
		for _, sub := range []bool{false, true} {
			rows = append(rows, map[string]any{"values": values, "subscriber": sub})
		}
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cli-beta-filter.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "TMPDIR=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLI_BETA_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file"}
	data, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var expected []struct{ Allowed, Warnings []string }
	if err = json.Unmarshal(out, &expected); err != nil || len(expected) != len(rows) {
		t.Fatal(err, string(out))
	}
	for i, row := range rows {
		allowed, warnings := Filter(row["values"].([]string), row["subscriber"].(bool))
		if len(warnings) == 0 {
			warnings = []string{}
		}
		if !reflect.DeepEqual(allowed, expected[i].Allowed) || !reflect.DeepEqual(warnings, expected[i].Warnings) {
			t.Fatal(i, allowed, warnings, expected[i])
		}
	}
}
