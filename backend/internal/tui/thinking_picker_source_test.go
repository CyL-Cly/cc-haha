package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestThinkingPickerSelectionAndConfirmationAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	type scenario struct {
		Current bool     `json:"current"`
		Mid     bool     `json:"mid"`
		Actions []string `json:"actions"`
	}
	rows := []scenario{}
	for _, current := range []bool{false, true} {
		for _, mid := range []bool{false, true} {
			for _, actions := range [][]string{{"enabled"}, {"disabled"}, {"enabled", "confirm"}, {"disabled", "confirm"}, {"enabled", "escape", "escape"}, {"disabled", "escape", "enabled", "confirm"}, {"escape"}} {
				rows = append(rows, scenario{Current: current, Mid: mid, Actions: actions})
			}
		}
	}
	home := t.TempDir()
	temp := filepath.Join(home, "tmp")
	_ = os.MkdirAll(temp, 0700)
	fixture, _ := filepath.Abs("testdata/thinking_picker.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TUI_THINKING_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("actual Source thinking component %v %.4000s", e, out)
	}
	type callback struct {
		Enabled   *bool
		Cancelled bool
	}
	var source struct {
		Rows []struct {
			Initial string
			Options []struct{ Value, Label, Description string }
			Stages  []struct {
				Confirmation bool
				Text         string
				Callbacks    []callback
			}
		}
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Rows) != len(rows) {
		t.Fatalf("Source shape %v %.4000s", e, out)
	}
	for i, row := range rows {
		observation := source.Rows[i]
		initial := "false"
		if row.Current {
			initial = "true"
		}
		if observation.Initial != initial || len(observation.Options) != 2 || observation.Options[0].Label != "Enabled" || observation.Options[1].Label != "Disabled" {
			t.Fatal("original Source options/current focus", observation)
		}
		state := NewState(nil, 120, 30)
		state.OpenThinkingPicker(ThinkingPicker{ID: "fixture", Current: row.Current, MidConversation: row.Mid})
		callbacks := []callback{}
		for j, step := range row.Actions {
			var actions []action
			if state.ThinkingPicker != nil {
				switch step {
				case "enabled", "disabled":
					state.ThinkingPicker.selected = 0
					if step == "disabled" {
						state.ThinkingPicker.selected = 1
					}
					actions = state.HandleKey(Key{Name: "enter"}, time.Now())
				case "confirm":
					if state.ThinkingPicker.pending != nil {
						actions = state.HandleKey(Key{Name: "enter"}, time.Now())
					}
				case "escape":
					actions = state.HandleKey(Key{Name: "esc"}, time.Now())
				}
			}
			for _, value := range actions {
				if value.kind == actionThinkingPicker {
					result := value.thinkingPickerResult
					if result.Cancelled {
						callbacks = append(callbacks, callback{Cancelled: true})
					} else {
						enabled := result.Enabled
						callbacks = append(callbacks, callback{Enabled: &enabled})
					}
				}
			}
			stage := observation.Stages[j]
			if !reflect.DeepEqual(callbacks, stage.Callbacks) {
				t.Errorf("case%d stage%d callback Go%v Source%v", i, j, callbacks, stage.Callbacks)
			}
			// Source rerenders before the parent unmounts; after selection the current
			// callback is what matters. Pending confirmation always remains mounted.
			if state.ThinkingPicker != nil {
				pending := state.ThinkingPicker.pending != nil
				if pending != stage.Confirmation {
					t.Errorf("case%d stage%d confirmation Go%v Source%v", i, j, pending, stage.Confirmation)
				}
				if pending && (!strings.Contains(stage.Text, "Do you want to proceed?") || !strings.Contains(state.View("", true).Content, "Do you want to proceed?")) {
					t.Error("Source warning not visible")
				}
			}
		}
	}
}
