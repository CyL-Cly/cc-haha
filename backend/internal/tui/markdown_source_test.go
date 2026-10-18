package tui

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func terminalMarkdownSourceCorpus() []string {
	return []string{
		"# First heading\n\nA **bold** and *italic* result with `code`.",
		"## Second heading\n\nFirst paragraph.\n\nSecond paragraph with [label](https://example.invalid/path).",
		"### Third heading\n\n> A quote with **strong** text.\n> Another line.",
		"- first item\n- second item\n  - nested item\n  - final nested item",
		"3. three\n4. four\n   1. child one\n   2. child two",
		"```go\nfunc hello() {\n  return\n}\n```\n\nAfter code.",
		"A hard break  \nnext line and a soft\nwrapped line.",
		"Escape \\*literal\\* and ~~removed~~ plus <b>HTML</b> text.",
		"[mail](mailto:owned@example.invalid) ![image](https://example.invalid/image.png)",
		"First\n\n---\n\nLast",
		"| Name | Count |\n| :--- | ---: |\n| Alpha | 2 |\n| **Beta** | 30 |",
		"Before table.\n\n| Head | Long description |\n| --- | --- |\n| One | a long multi word column for narrow wrapping |\n| Two | another longer value |\n\nAfter table.",
		"| Label | Description |\n| --- | --- |\n| A | This deliberately very long description has many words and keeps going until the narrow table changes to a vertical list for the human reader. |",
		"<context>hidden context</context>\nVisible &amp; &#x41; text with 中文 😀.",
	}
}

func TestTerminalLocalOutputOriginalMountedMarkdownBlocksInlineAndTables(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source required")
	}
	rows := []map[string]any{}
	contents := append(terminalMarkdownSourceCorpus(), "◇ Owned session · launching", "◆ Completed session · done\nA **literal** result with `ticks` and another long line for wrapping.", "◇ Wrapped long session label and longer suffix · waiting for the remote result\n\nFirst line\nsecond line")
	for _, width := range []int{100, 47, 24} {
		for _, content := range contents {
			rows = append(rows, map[string]any{"width": width, "text": "<local-command-stdout>" + content + "</local-command-stdout>"})
		}
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-local-user-render.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_LOCAL_USER_RENDER_SOURCE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=100", "LINES=35"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var expected []struct{ Text string }
	if err = json.Unmarshal(out, &expected); err != nil || len(expected) != len(rows) {
		t.Fatal(err, string(out))
	}
	normalize := func(value string) string {
		lines := strings.Split(ansi.Strip(value), "\n")
		for i := range lines {
			lines[i] = strings.TrimRight(lines[i], " ")
		}
		return strings.Trim(strings.Join(lines, "\n"), "\n")
	}
	for i, row := range rows {
		actual, _ := terminalLocalUserMessageLines(row["text"].(string), row["width"].(int), "dark")
		got, want := normalize(strings.Join(actual, "\n")), normalize(expected[i].Text)
		if got != want {
			t.Errorf("Actual Markdown row%d width%d\nGo:%q\nSource:%q", i, row["width"], got, want)
		}
	}
}

func TestTerminalMarkdownOriginalStaticAnsiSixThemesGeometryAndStyles(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source required")
	}
	rows := []map[string]any{}
	for _, theme := range []string{"dark", "light", "dark-daltonized", "light-daltonized", "dark-ansi", "light-ansi"} {
		for _, width := range []int{100, 47, 24} {
			for _, content := range terminalMarkdownSourceCorpus() {
				rows = append(rows, map[string]any{"width": width, "theme": theme, "markdown": true, "text": content, "nativeRows": TerminalMarkdownRows(content, width, width, theme)})
			}
		}
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-local-user-render.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_LOCAL_USER_RENDER_SOURCE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=100", "LINES=35", "FORCE_COLOR=3"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var expected []struct {
		Original           string
		Cells, NativeCells []json.RawMessage
	}
	if err = json.Unmarshal(out, &expected); err != nil || len(expected) != len(rows) {
		t.Fatal(err, string(out))
	}
	for i, result := range expected {
		left, _ := json.Marshal(result.Cells)
		right, _ := json.Marshal(result.NativeCells)
		if string(left) != string(right) {
			first := 0
			for first < len(result.Cells) && first < len(result.NativeCells) && string(result.Cells[first]) == string(result.NativeCells[first]) {
				first++
			}
			sourceCell, nativeCell := json.RawMessage(`null`), json.RawMessage(`null`)
			if first < len(result.Cells) {
				sourceCell = result.Cells[first]
			}
			if first < len(result.NativeCells) {
				nativeCell = result.NativeCells[first]
			}
			t.Errorf("Original Markdown cell/style row%d theme%s width%d first%d SourceCells%d GoCells%d Source%q Go%q\nSourceCell%s\nGoCell%s", i, rows[i]["theme"], rows[i]["width"], first, len(result.Cells), len(result.NativeCells), result.Original, ansi.Strip(strings.Join(rows[i]["nativeRows"].([]string), "\n")), sourceCell, nativeCell)
		}
	}
	t.Logf("Actual Markdown/staticRender profiles=%d", len(rows))
}
