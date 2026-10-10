package tui

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWorktreeExitOriginalReactComponentProps(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	profiles := []WorktreeExitPrompt{{Path: "/private/w", Branch: "branch", ChangedFiles: []string{}}, {Path: "/private/w", Branch: "branch", ChangedFiles: []string{" M file"}}, {Path: "/private/w", Branch: "branch", Commits: 1, ChangedFiles: []string{}}, {Path: "/private/w", Branch: "branch", Commits: 4, ChangedFiles: []string{" M a", "?? b"}}, {Path: "/private/w", Branch: "branch", Tmux: "tmux", Commits: 2, ChangedFiles: []string{" M a"}}}
	rows := []map[string]any{}
	for _, profile := range profiles {
		rows = append(rows, map[string]any{"path": profile.Path, "branch": profile.Branch, "tmux": profile.Tmux, "files": profile.ChangedFiles, "commits": profile.Commits})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-worktree-exit-ui.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WORKTREE_EXIT_UI_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source UI %v %.2500s", e, stderr.String())
	}
	var source struct {
		Rows []struct {
			Subtitle, DefaultValue string
			Options                []struct{ Value, Label, Description string }
		}
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(output, &source); e != nil || len(source.Rows) != len(profiles) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source UI isolation %v %.2500s", e, output)
	}
	for i, profile := range profiles {
		row := source.Rows[i]
		if row.Subtitle != profile.subtitle() {
			t.Errorf("subtitle %d Go%s Source%s", i, profile.subtitle(), row.Subtitle)
		}
		choices := profile.choices()
		if len(choices) != len(row.Options) {
			t.Fatal("Source choices count", i)
		}
		for j, choice := range choices {
			if !reflect.DeepEqual([]string{choice.value, choice.label, choice.description}, []string{row.Options[j].Value, row.Options[j].Label, row.Options[j].Description}) {
				t.Errorf("Source option differs %d/%d", i, j)
			}
		}
		if row.DefaultValue != choices[0].value {
			t.Fatal("Source default keep differs", i)
		}
	}
}
