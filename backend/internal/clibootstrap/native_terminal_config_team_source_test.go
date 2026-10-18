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
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

type terminalTeamConfigStep struct {
	Kind  string  `json:"kind"`
	Value *string `json:"value"`
}
type terminalTeamConfigTrace struct {
	Global   protocol.JSONObject `json:"global"`
	Mode     string              `json:"mode"`
	Override *string             `json:"override"`
	Dirty    bool                `json:"dirty"`
}

func TestTerminalConfigTeamsActualSourceCallbacksModeSnapshotDefaultModelAndEscape(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun required")
	}
	ctx := context.Background()
	rows := []struct {
		cli    string
		global map[string]any
		steps  []terminalTeamConfigStep
	}{
		{cli: "tmux", global: map[string]any{}, steps: []terminalTeamConfigStep{{"mode", pickerPointer("tmux")}, {"mode", pickerPointer("in-process")}, {"escape", nil}}},
		{global: map[string]any{"teammateMode": "in-process"}, steps: []terminalTeamConfigStep{{"mode", pickerPointer("auto")}, {"external-global-write", pickerPointer("tmux")}, {"escape", nil}}},
		{global: map[string]any{}, steps: []terminalTeamConfigStep{{"model", nil}, {"escape", nil}}},
		{global: map[string]any{}, steps: []terminalTeamConfigStep{{"model", pickerPointer("haiku")}, {"escape", nil}}},
		{global: map[string]any{"teammateDefaultModel": nil}, steps: []terminalTeamConfigStep{{"model", nil}, {"escape", nil}}},
		{global: map[string]any{"teammateDefaultModel": "haiku"}, steps: []terminalTeamConfigStep{{"model", pickerPointer("haiku")}, {"model", nil}, {"escape", nil}}},
	}
	var originalRows []any
	var actual [][]terminalTeamConfigTrace
	for _, row := range rows {
		m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
			n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
			value := map[string]any{"hasCompletedOnboarding": true, "futureTeamConfig": map[string]any{"keep": 17}}
			for key, v := range row.global {
				value[key] = v
			}
			if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], ".claude.json"), rawMain(value), 0600); err != nil {
				t.Fatal(err)
			}
		}, func(o *MainPrintOptions) { o.Interactive = true; o.Arguments.TeammateMode = row.cli })
		snapshot, err := m.TerminalConfigState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		initial := mainObj(snapshot.Get("global"))
		originalRows = append(originalRows, map[string]any{"cli": row.cli, "global": initial, "steps": row.steps})
		dirty := map[string]bool{}
		trace := func() terminalTeamConfigTrace {
			global, err := m.Global.Get()
			if err != nil {
				t.Fatal(err)
			}
			var override *string
			if m.options.Arguments.TeammateMode != "" {
				override = pickerPointer(m.options.Arguments.TeammateMode)
			}
			return terminalTeamConfigTrace{Global: global.Clone(), Mode: m.teamLeader.mode, Override: override, Dirty: len(dirty) > 0}
		}
		traces := []terminalTeamConfigTrace{trace()}
		for _, step := range row.steps {
			switch step.Kind {
			case "mode":
				if err = m.SetTerminalConfigSetting(ctx, "teammateMode", rawMain(step.Value)); err != nil {
					t.Fatal(err)
				}
				dirty["teammateMode"] = true
			case "model":
				picker, err := m.NativeTeammateModelPicker(ctx)
				if err != nil {
					t.Fatal(err)
				}
				didChange, err := m.ApplyNativeTeammateModelPicker(ctx, picker.ID, tui.ModelPickerResult{Model: step.Value, Effort: pickerPointer("high"), HasToggledEffort: true})
				if err != nil {
					t.Fatal(err)
				}
				if didChange {
					dirty["teammateDefaultModel"] = true
				}
			case "external-global-write":
				err = m.Owner.Run(ctx, func(*cooperative.Scope) error {
					return m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
						next := current.Clone()
						next.Set("teammateMode", rawMain(step.Value))
						return &next, nil
					})
				})
				if err != nil {
					t.Fatal(err)
				}
			case "escape":
				keys := []string{}
				for key := range dirty {
					keys = append(keys, key)
				}
				if len(keys) > 0 {
					if err = m.RestoreTerminalConfig(ctx, snapshot, keys); err != nil {
						t.Fatal(err)
					}
				}
			}
			traces = append(traces, trace())
		}
		actual = append(actual, traces)
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-config-team.fixture.ts")
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_CONFIG_TEAM_SOURCE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "rows": originalRows}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Source: %v %.9000s", err, output)
	}
	var original [][]terminalTeamConfigTrace
	if err = json.Unmarshal(output, &original); err != nil {
		t.Fatal(err, string(output))
	}
	if len(actual) != len(original) {
		t.Fatal("Source trace count", len(original))
	}
	for row := range actual {
		if len(actual[row]) != len(original[row]) {
			t.Fatal("Source step count", row)
		}
		for step := range actual[row] {
			var got, want any
			_ = json.Unmarshal(rawMain(actual[row][step]), &got)
			_ = json.Unmarshal(rawMain(original[row][step]), &want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("row %d step %d: Go %s\nSource %s", row, step, rawMain(got), rawMain(want))
			}
		}
	}
}
