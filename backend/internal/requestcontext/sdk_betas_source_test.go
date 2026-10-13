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
)

func TestActualSourceMergedSDKBetasPreserveOfficialDuplicatesAndRelaySet(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []map[string]any{}
	for _, provider := range []string{"firstParty", "vertex", "bedrock", "foundry"} {
		for _, custom := range []bool{false, true} {
			for _, base := range [][]string{{}, {"existing"}, {"context-1m-2025-08-07"}} {
				for _, disable := range []bool{false, true} {
					rows = append(rows, map[string]any{"provider": provider, "custom": custom, "base": base, "environment": base, "sdk": []string{"context-1m-2025-08-07", "context-1m-2025-08-07"}, "env": map[string]string{"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": map[bool]string{true: "1", false: "0"}[disable]}, "has1m": true})
				}
			}
		}
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cli-beta-filter.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLI_BETA_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file"}
	data, _ := json.Marshal(map[string]any{"home": home, "merged": rows})
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var expected [][]string
	if err = json.Unmarshal(out, &expected); err != nil {
		t.Fatal(err, string(out))
	}
	for i, row := range rows {
		custom := row["provider"] == "firstParty" && row["custom"].(bool)
		base := row["base"].([]string)
		if custom && row["env"].(map[string]string)["CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"] != "1" {
			base = append([]string{"context-1m-2025-08-07"}, base...)
		}
		actual := MergeSDKBetas(base, row["sdk"].([]string), custom)
		if !reflect.DeepEqual(actual, expected[i]) {
			t.Error(i, actual, expected[i])
		}
	}
}
