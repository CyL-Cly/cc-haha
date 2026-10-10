package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestTeamsEnabledOrdinaryPlanHintUsesActualRegisteredAgentAgainstSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	rows := []map[string]any{}
	actual := []bool{}
	for _, enabled := range []bool{false, true} {
		m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
			n.Environment["CC_HAHA_TEAM_REVIEW_REQUIRED"] = "1"
			n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "0"
			if enabled {
				n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
			}
		}, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"EnterPlanMode,ExitPlanMode,Read"} })
		if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			has, err := m.nativePlanModeBindings().HasTaskTool(scope)
			if err != nil {
				return err
			}
			actual = append(actual, has)
			_, registered, err := m.nativeTools.Catalog(scope)
			if err != nil {
				return err
			}
			names := []string{}
			for _, tool := range registered {
				names = append(names, tool.Name)
			}
			rows = append(rows, map[string]any{"enabled": enabled, "names": names})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-plan.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_PLAN_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": home, "taskHints": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(output))
	}
	var source struct {
		Observations []bool
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output), stderr.String())
	}
	if !reflect.DeepEqual(actual, source.Observations) {
		t.Fatalf("Go%v Source%v", actual, source.Observations)
	}
}
