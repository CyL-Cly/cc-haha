package queryhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestComputerUseAbortBranchesMatchActualSourceCleanup(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cu-abort-cleanup.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	type profile struct{ Child, Cancelled, OverlayFailure, BadgeFailure bool }
	profiles := []profile{}
	for _, child := range []bool{false, true} {
		for _, cancelled := range []bool{false, true} {
			for _, overlay := range []bool{false, true} {
				for _, badge := range []bool{false, true} {
					profiles = append(profiles, profile{child, cancelled, overlay, badge})
				}
			}
		}
	}
	// JSON names are original fixture inputs, not captured cleanup outputs.
	inputs := []map[string]bool{}
	for _, p := range profiles {
		inputs = append(inputs, map[string]bool{"child": p.Child, "cancelled": p.Cancelled, "overlayFailure": p.OverlayFailure, "badgeFailure": p.BadgeFailure})
	}
	raw, _ := json.Marshal(map[string]any{"homeDir": home, "profiles": inputs})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CU_ABORT_CLEANUP_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "PATH=/usr/bin:/bin", "DISABLE_TELEMETRY=1"}
	cmd.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Branches int
		Rows     []struct {
			Calls     int
			Phases    []string
			Cancelled bool
		}
	}
	if err = json.Unmarshal(output, &source); err != nil {
		t.Fatal(err, string(output))
	}
	if source.Branches != 2 || len(source.Rows) != len(profiles)*2 {
		t.Fatal("Actual Source abort coverage", string(output))
	}
	service, _, _ := newTestService(t, nil)
	for i, row := range source.Rows {
		p := profiles[i%len(profiles)]
		call := emptyContext()
		call.ComputerUseInactive = false
		if p.Child {
			call.AgentID = "child-agent"
		}
		caller, abort := context.WithCancel(context.Background())
		if p.Cancelled {
			abort()
		}
		calls := 0
		call.CleanupComputerUse = func(cleanup context.Context) error {
			calls++
			if cleanup.Err() != nil {
				t.Error("Source cleanup inherited canceled signal")
			}
			if p.BadgeFailure {
				return errors.New("badge failure")
			}
			return nil
		}
		if err = service.AbortCleanup(caller, call); err != nil {
			t.Fatalf("row%d %v", i, err)
		}
		abort()
		// The callback-count/error comparison proves the Go consumer. These
		// separate phase assertions constrain the real imported Source resource
		// producer; Go's native lease/daemon lifecycle is exercised in its own
		// physical and actual MainPrint tests, not simulated by this callback.
		phases := []string{}
		if !p.Child {
			phases = append(phases, "badge")
			if !p.BadgeFailure {
				phases = append(phases, "overlay", "state")
			}
		}
		if calls != row.Calls || !reflect.DeepEqual(phases, row.Phases) || p.Cancelled != row.Cancelled {
			t.Errorf("row%d Go calls%d phases%v vs Source%+v", i, calls, phases, row)
		}
	}
}
