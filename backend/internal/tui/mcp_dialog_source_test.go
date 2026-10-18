package tui

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestMCPDoubleExitOriginalMountedHookIndependentKeysTimeoutAndUnmount(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual MCP Source hook required")
	}
	type step struct {
		At      int    `json:"at"`
		Key     string `json:"key,omitempty"`
		Unmount bool   `json:"unmount,omitempty"`
	}
	profiles := [][]step{{{0, "ctrl+c", false}, {100, "ctrl+c", false}}, {{0, "ctrl+d", false}, {100, "ctrl+d", false}}, {{0, "ctrl+c", false}, {100, "ctrl+d", false}, {200, "ctrl+c", false}}, {{0, "ctrl+c", false}, {801, "ctrl+c", false}, {900, "ctrl+c", false}}, {{0, "ctrl+d", false}, {100, "", true}, {200, "ctrl+d", false}, {300, "ctrl+d", false}}}
	inputProfiles := []map[string]any{}
	for _, steps := range profiles {
		inputProfiles = append(inputProfiles, map[string]any{"steps": steps})
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-mcp-exit.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_MCP_EXIT_SOURCE=1", "BUN_OPTIONS=--no-env-file"}
	input, _ := json.Marshal(map[string]any{"home": home, "profiles": inputProfiles})
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual MCP mounted exit hook %v %.5000s", err, output)
	}
	var source struct {
		Mounts       []string
		Observations []struct {
			Steps []struct {
				Pending bool
				KeyName string
				Exits   int
			}
			TimersAfterUnmount int
		}
	}
	if err = json.Unmarshal(output, &source); err != nil {
		t.Fatal(err, string(output))
	}
	if len(source.Mounts) != 6 || len(source.Observations) != len(profiles) {
		t.Fatal(string(output))
	}
	for i, steps := range profiles {
		s := NewState(nil, 80, 24)
		panel := LocalPanel{ID: "actual", Kind: "mcp", Title: "MCP", MCPExitScope: "mounted"}
		s.Apply(Event{Kind: LocalPanelOpened, LocalPanel: &panel})
		exits := 0
		now := time.Now()
		for j, step := range steps {
			if step.Unmount {
				s.Apply(Event{Kind: LocalPanelClosed})
				s.Apply(Event{Kind: LocalPanelOpened, LocalPanel: &panel})
			}
			if step.Key != "" {
				for _, action := range s.HandleKey(Key{Name: step.Key}, now.Add(time.Duration(step.At)*time.Millisecond)) {
					if action.kind == actionQuit {
						exits++
					}
				}
			}
			if s.LocalPanel == nil || exits != source.Observations[i].Steps[j].Exits {
				t.Fatalf("profile%d step%d actual exits%d panel%v Source%+v", i, j, exits, s.LocalPanel, source.Observations[i].Steps[j])
			}
		}
		if source.Observations[i].TimersAfterUnmount != 0 {
			t.Fatal("original unmount leaked a timer", source.Observations[i])
		}
	}
}
