package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFallbackToolErrorsAgainstActualSourceComponent(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	temp := filepath.Join(home, "tmp")
	_ = os.MkdirAll(temp, 0700)
	_ = os.MkdirAll(filepath.Join(home, ".git"), 0700)
	type errorCase struct {
		Result  any  `json:"result"`
		Verbose bool `json:"verbose"`
	}
	cases := []errorCase{{"actual stderr", false}, {"Error: original", false}, {"Cancelled: original", true}, {" \ufeff<error>InputValidationError: actual field</error> \ufeff", false}, {"InputValidationError: actual field", true}, {[]any{map[string]any{"type": "text", "text": "not a Source string"}}, false}, {"", false}, {"<tool_use_error reason='original'>wrapped\nactual</tool_use_error><sandbox_violations>private</sandbox_violations>", false}, {"<sandbox_violations>private\ntrace</sandbox_violations><error>actual</error>", true}, {strings.Repeat("line\n", 13), false}, {strings.Repeat("line\n", 13), true}, {"<tool_use_error></tool_use_error>", false}, {"<TOOL_USE_ERROR>case insensitive</TOOL_USE_ERROR>", false}, {"<tool_use_error>first<tool_use_error>inner</tool_use_error>end</tool_use_error>", false}}
	type windowCase struct {
		Suggestions []map[string]any `json:"suggestions"`
		Selected    int              `json:"selected"`
		Overlay     bool             `json:"overlay"`
	}
	windows := []windowCase{}
	for _, count := range []int{0, 1, 4, 12} {
		for _, selected := range []int{0, count / 2, max(0, count-1)} {
			for _, overlay := range []bool{false, true} {
				w := windowCase{Suggestions: []map[string]any{}, Selected: selected, Overlay: overlay}
				for i := 0; i < count; i++ {
					w.Suggestions = append(w.Suggestions, map[string]any{"id": fmt.Sprintf("item%d", i), "displayText": fmt.Sprintf("/item%d", i), "description": "actual"})
				}
				windows = append(windows, w)
			}
		}
	}
	fixture, _ := filepath.Abs("testdata/presentation.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TUI_PRESENTATION_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"home": home, "reads": []any{}, "reminders": []string{}, "errors": cases, "windows": windows, "height": 30})
	cmd.Stdin = bytes.NewReader(input)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("actual Source component %v %.4000s", e, out)
	}
	var source struct {
		Errors  []string
		Windows [][]struct {
			Name     string
			Selected bool
		}
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Errors) != len(cases) {
		t.Fatalf("Source shape %v %.4000s", e, out)
	}
	for i, c := range cases {
		raw, _ := json.Marshal(c.Result)
		if got := toolErrorPresentation(raw, c.Verbose); got != source.Errors[i] {
			t.Errorf("case%d Go%q Source%q", i, got, source.Errors[i])
		}
	}
	if len(source.Windows) != len(windows) {
		t.Fatal("Source missing command windows")
	}
	for i, w := range windows {
		start, end := completionWindow(len(w.Suggestions), w.Selected, 30, w.Overlay)
		got := source.Windows[i]
		if len(got) != end-start {
			t.Errorf("window%d Go%d:%d Source%v", i, start, end, got)
			continue
		}
		for j, row := range got {
			if row.Name != fmt.Sprintf("item%d", start+j) || row.Selected != (start+j == w.Selected) {
				t.Errorf("window%d row%d Go%d Source%v", i, j, start+j, row)
			}
		}
	}
}
