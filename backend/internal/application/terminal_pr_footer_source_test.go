package application

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestTerminalPRFooterOldGlobalValuesMatchActualOriginalGetter(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual pinned Source Bun required")
	}
	rows := []protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "future", Value: raw(true)})}
	for _, value := range []any{nil, true, false, 0, "", "false", []any{}, map[string]any{}} {
		rows = append(rows, protocol.NewJSONObject(protocol.JSONField{Name: "prStatusFooterEnabled", Value: raw(value)}, protocol.JSONField{Name: "future", Value: raw(true)}))
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-pr-footer.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_PR_SOURCE=1"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "globalSettings": rows}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual original PR getter %v %.5000s", err, output)
	}
	var source struct{ SettingEnabled []bool }
	if err = json.Unmarshal(output, &source); err != nil || len(source.SettingEnabled) != len(rows) {
		t.Fatal(err, string(output))
	}
	actual := []bool{}
	for _, row := range rows {
		before := raw(row)
		snapshot := protocol.NewJSONObject(protocol.JSONField{Name: "global", Value: before})
		actual = append(actual, terminalPreferences(snapshot).PRStatusFooterEnabled)
		if !reflect.DeepEqual(before, raw(row)) || string(row.Get("future")) != "true" {
			t.Fatal("old global getter mutated unknown fields")
		}
	}
	if !reflect.DeepEqual(actual, source.SettingEnabled) {
		t.Fatal("actual Source nullish/truthy setting", actual, source.SettingEnabled)
	}
}
