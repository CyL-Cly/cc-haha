package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/terminaldiff"
)

func TestActualWholeDiffDialogSourceKeyboardSourcesDetailsAndDismiss(t *testing.T) {
	file := terminaldiff.File{Path: "changed.txt", LinesAdded: 1, LinesRemoved: 1, Hunks: []terminaldiff.Hunk{{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1, Lines: []string{"-old", "+new"}}}}
	dialog := terminaldiff.Dialog{Sources: []terminaldiff.Source{{Data: terminaldiff.Data{Stats: &terminaldiff.Stats{FilesCount: 2, LinesAdded: 1, LinesRemoved: 1}, Files: []terminaldiff.File{file, {Path: "untracked.txt", IsUntracked: true}}}}, {TurnIndex: 3, UserPromptPreview: "change file", Data: terminaldiff.Data{Stats: &terminaldiff.Stats{FilesCount: 1, LinesAdded: 1, LinesRemoved: 1}, Files: []terminaldiff.File{file}}}}}
	actions := []any{"diff:nextFile", "diff:nextFile", "diff:viewDetails", "diff:nextSource", "diff:previousFile", "diff:previousSource", "diff:nextSource", "diff:viewDetails", map[string]any{"resolveCurrent": map[string]any{"stats": dialog.Sources[0].Data.Stats, "files": dialog.Sources[0].Data.Files, "hunks": map[string]any{file.Path: file.Hunks}, "loading": false}}, "diff:dismiss", "diff:previousSource", "diff:previousFile", "diff:dismiss"}
	keys := []Key{{Name: "down"}, {Name: "down"}, {Name: "enter"}, {Name: "right"}, {Name: "up"}, {Name: "left"}, {Name: "right"}, {Name: "enter"}, {Name: "fixture-resolve-current"}, {Name: "esc"}, {Name: "left"}, {Name: "up"}, {Name: "esc"}}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned Source Bun required")
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-diff.fixture.ts")
	current := map[string]any{"stats": dialog.Sources[0].Data.Stats, "files": dialog.Sources[0].Data.Files, "hunks": map[string]any{file.Path: file.Hunks}, "loading": false}
	turns := []any{map[string]any{"turnIndex": 3, "userPromptPreview": "change file", "files": map[string]any{file.Path: map[string]any{"filePath": file.Path, "hunks": file.Hunks, "linesAdded": 1, "linesRemoved": 1, "isNewFile": false}}, "stats": map[string]any{"filesChanged": 1, "linesAdded": 1, "linesRemoved": 1}}}
	input, _ := json.Marshal(map[string]any{"home": home, "dialogs": []any{map[string]any{"current": current, "turns": turns, "actions": actions}}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_DIFF_SOURCE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("actual Source DiffDialog %v %s", e, stderr.String())
	}
	var result struct{ Dialogs [][]protocol.JSONObject }
	if e = json.Unmarshal(out, &result); e != nil {
		t.Fatal(e, string(out))
	}
	s := NewState(nil, 80, 40)
	s.Width = 80
	s.Height = 40
	s.Editor.Set("preserved draft")
	encoded, _ := json.Marshal(dialog)
	s.openLocalPanel(&LocalPanel{ID: "original-dialog", Kind: "diff", Presentation: encoded})
	compare := func(i int) {
		t.Helper()
		expected := result.Dialogs[0][i]
		if s.LocalPanel == nil {
			if !strings.Contains(string(expected.Get("completed")), "Diff dialog dismissed") {
				t.Fatal("premature native close", i)
			}
			return
		}
		p := s.LocalPanel
		view := "list"
		if p.detail {
			view = "detail"
		}
		var index, selected int
		_ = json.Unmarshal(expected.Get("sourceIndex"), &index)
		_ = json.Unmarshal(expected.Get("selectedIndex"), &selected)
		if p.scroll != index || p.selected != selected || view != expected.String("viewMode") {
			t.Fatalf("step %d native source/file/detail %d/%d/%s vs Source %s", i, p.scroll, p.selected, view, expected.Get("viewMode"))
		}
	}
	compare(0)
	for i, key := range keys {
		if key.Name == "fixture-resolve-current" {
			s.Apply(Event{Kind: LocalPanelRefreshed, LocalPanel: &LocalPanel{ID: "original-dialog", Kind: "diff", Presentation: encoded}})
		} else {
			_ = s.HandleKey(key, time.Unix(0, int64(i+1)))
		}
		compare(i + 1)
	}
	if s.Editor.Text() != "preserved draft" {
		t.Fatal("dialog modified draft")
	}
}
