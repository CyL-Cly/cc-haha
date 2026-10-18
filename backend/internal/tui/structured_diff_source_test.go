package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/terminaldiff"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestActualSourceStructuredDiffFallbackWordHighlightsNumbersAndWrapping(t *testing.T) {
	hunks := []terminaldiff.Hunk{{OldStart: 1, Lines: []string{" function greet() {", `-  console.log("Hello, World!");`, `+  console.log("Hello, Claude!");`, " }"}}, {OldStart: 98, Lines: []string{"-old one", "-old two", "-last old", "+new one", "+new two", " context", "", "+"}}, {OldStart: 0, Lines: []string{"-a", "+this is entirely different"}}, {OldStart: 9, Lines: []string{" 中文🙂 code", "-  café old name", "+  café new name"}}}
	type sample struct {
		Hunk  terminaldiff.Hunk `json:"hunk"`
		Width int               `json:"width"`
		Theme string            `json:"theme"`
	}
	cases := []sample{}
	for _, h := range hunks {
		for _, width := range []int{1, 7, 13, 24, 38, 80} {
			cases = append(cases, sample{h, width, "dark"})
		}
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned Source Bun required")
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-diff.fixture.ts")
	input, _ := json.Marshal(map[string]any{"home": home, "rendered": cases})
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
		t.Fatalf("actual Source fallback %v %s", e, stderr.String())
	}
	var source struct{ Rendered [][][]TerminalThemeSpan }
	if e = json.Unmarshal(out, &source); e != nil {
		t.Fatal(e, string(out))
	}
	if len(source.Rendered) != len(cases) {
		t.Fatal("missing original render")
	}
	for i, item := range cases {
		got := TerminalStructuredDiffRows(item.Hunk, item.Width, item.Theme)
		if !reflect.DeepEqual(got, source.Rendered[i]) {
			a, _ := json.Marshal(got)
			b, _ := json.Marshal(source.Rendered[i])
			t.Errorf("hunk %d width %d\nGo %s\nSource %s", i/6, item.Width, a, b)
		}
	}
}
