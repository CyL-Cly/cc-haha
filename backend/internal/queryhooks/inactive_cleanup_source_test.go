package queryhooks

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

func TestInactiveComputerUseAbortStillConsumesActualSourceCleanup(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	for _, child := range []bool{false, true} {
		for _, cancelled := range []bool{false, true} {
			home := t.TempDir()
			_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cu-inactive-cleanup.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			command.Dir = home
			command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "PATH=/usr/bin:/bin", "NODE_ENV=production", "USER_TYPE=external", "CLAUDE_CODE_SIMPLE=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CU_INACTIVE_CLEANUP=1", "BUN_OPTIONS=--no-env-file"}
			input, _ := json.Marshal(map[string]any{"home": home, "child": child, "cancelled": cancelled})
			command.Stdin = bytes.NewReader(input)
			var stderr bytes.Buffer
			command.Stderr = &stderr
			output, err := command.Output()
			cancel()
			if err != nil {
				t.Fatal(err, string(output), stderr.String())
			}
			var expected struct {
				Rows []struct {
					StateReads, Timers, TimersCleared int
					Kind                              string
					Phases                            []string
				}
				Inactive bool
			}
			if err = json.Unmarshal(output, &expected); err != nil || len(expected.Rows) != 3 || !expected.Inactive {
				t.Fatal(err, string(output))
			}
			service, _, _ := newTestService(t, nil)
			for _, row := range expected.Rows {
				call := emptyContext()
				call.ComputerUseInactive = true
				if child {
					call.AgentID = "child-agent"
				}
				caller, abort := context.WithCancel(context.Background())
				if cancelled {
					abort()
				}
				reads := 0
				call.CleanupComputerUse = func(cleanup context.Context) error {
					if cleanup.Err() != nil {
						t.Fatal("model cancellation reached inactive cleanup")
					}
					reads++
					return nil
				}
				if row.Kind == "stop" {
					_, err = service.StopHooks(context.WithoutCancel(caller), call, rejectYield)
				} else {
					err = service.AbortCleanup(caller, call)
				}
				if err != nil {
					t.Fatal(err)
				}
				abort()
				if reads != row.StateReads {
					t.Fatalf("inactive actual cleanup was skipped: Go=%d Source=%d child=%v cancelled=%v phases=%v", reads, row.StateReads, child, cancelled, row.Phases)
				}
				if !child && (row.Timers != 1 || row.TimersCleared != 1) {
					t.Fatal("actual Source inactive backstop lifecycle", string(output))
				}
			}
		}
	}
}
