package tui

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

func TestActualSourceExitAliasImmediateGoodbyeAndWorktreeBranch(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []map[string]any{}
	for _, r := range []float64{0, .249, .25, .5, .75, .999} {
		rows = append(rows, map[string]any{"random": r, "worktree": false})
	}
	rows = append(rows, map[string]any{"random": .5, "worktree": true})
	inputs := []string{"/exit", "/quit", " /exit ", "/exit ignored arguments", "/quit ignored arguments", "exit", "quit", "/EXIT", "/Quit", "/exitx", "/exit\targuments", "/quitx"}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-exit-command.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_EXIT_COMMAND_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	raw, _ := json.Marshal(map[string]any{"rows": rows, "inputs": inputs})
	cmd.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Metadata struct {
			Name, Type string
			Aliases    []string
			Immediate  bool
		}
		Inputs []bool
		Rows   []struct {
			Events []struct {
				Kind   string
				Args   []string
				Code   int
				Reason string
			}
			ReturnedNull bool
			WorktreeView *struct{ ShowWorktree, OnCancel bool }
		}
		HomeOwned bool
	}
	if err = json.Unmarshal(out, &source); err != nil {
		t.Fatal(err, string(out))
	}
	if !source.HomeOwned || source.Metadata.Name != "exit" || source.Metadata.Type != "local-jsx" || !source.Metadata.Immediate || len(source.Metadata.Aliases) != 1 || source.Metadata.Aliases[0] != "quit" {
		t.Fatal("actual exit registration changed", source.Metadata)
	}
	for i, input := range inputs {
		s := NewState(nil, 80, 24)
		s.Editor.Set(input)
		actions := s.HandleKey(Key{Name: "enter"}, time.Now())
		quit := len(actions) == 1 && actions[0].kind == actionQuit
		if quit != source.Inputs[i] {
			t.Fatalf("actual Source exit parse %q: Go=%v Source=%v", input, quit, source.Inputs[i])
		}
	}
	for i, row := range source.Rows {
		if i == len(rows)-1 {
			if row.ReturnedNull || len(row.Events) != 0 || row.WorktreeView == nil || !row.WorktreeView.ShowWorktree || !row.WorktreeView.OnCancel {
				t.Fatal("Source worktree bypassed choice", row)
			}
			continue
		}
		if !row.ReturnedNull || row.WorktreeView != nil || len(row.Events) != 2 || row.Events[0].Kind != "done" || len(row.Events[0].Args) != 1 || row.Events[0].Args[0] != exitGoodbye(rows[i]["random"].(float64)) || row.Events[1].Kind != "shutdown" || row.Events[1].Code != 0 || row.Events[1].Reason != "prompt_input_exit" {
			t.Fatal("actual Source exit ordering/reason/goodbye", i, row)
		}
	}
}
