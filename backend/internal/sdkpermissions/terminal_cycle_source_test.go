package sdkpermissions

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestTerminalOrdinaryPermissionCycleAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	type row struct {
		Mode, UserType string
		Bypass, Auto   bool
	}
	rows := []row{}
	for _, mode := range []string{"default", "acceptEdits", "plan", "bypassPermissions", "dontAsk", "auto", "futureMode"} {
		for _, user := range []string{"external", "ant"} {
			for _, bypass := range []bool{false, true} {
				for _, auto := range []bool{false, true} {
					rows = append(rows, row{mode, user, bypass, auto})
				}
			}
		}
	}
	for _, compiled := range []bool{false, true} {
		for _, live := range []bool{false, true} {
			home := t.TempDir()
			tmp := filepath.Join(home, "tmp")
			_ = os.MkdirAll(tmp, 0700)
			fixture, _ := filepath.Abs("terminal_cycle.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			args := []string{"--no-env-file"}
			if compiled {
				args = append(args, "--feature=TRANSCRIPT_CLASSIFIER")
			}
			args = append(args, fixture)
			cmd := exec.CommandContext(ctx, bun, args...)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + tmp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_CYCLE_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
			input, _ := json.Marshal(map[string]any{"home": home, "rows": rows, "live": live})
			cmd.Stdin = bytes.NewReader(input)
			out, e := cmd.CombinedOutput()
			cancel()
			if e != nil {
				t.Fatalf("actual Source cycle %v %.3000s", e, out)
			}
			var source struct{ Rows []string }
			if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(rows) {
				t.Fatalf("Source shape %v %.3000s", e, out)
			}
			for i, row := range rows {
				context := protocol.NewJSONObject(protocol.JSONField{Name: "mode", Value: quote(row.Mode)}, protocol.JSONField{Name: "isBypassPermissionsModeAvailable", Value: persistRaw(row.Bypass)}, protocol.JSONField{Name: "isAutoModeAvailable", Value: persistRaw(row.Auto)}, protocol.JSONField{Name: "unknown", Value: json.RawMessage(`{"preserved":true}`)})
				before, _ := context.MarshalJSON()
				actual := NextTerminalMode(context, row.UserType, compiled && live)
				after, _ := context.MarshalJSON()
				if actual != source.Rows[i] || !bytes.Equal(before, after) {
					t.Errorf("compiled%v case%d Source%s Go%s contextChanged%v", compiled, i, source.Rows[i], actual, !bytes.Equal(before, after))
				}
			}
		}
	}
}
