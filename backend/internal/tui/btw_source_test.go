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

	"github.com/charmbracelet/x/ansi"
)

func TestTerminalBtwActualSourceMountedPlainResponseErrorAndNarrowHeader(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Actual pinned Source required")
	}
	rows := []map[string]any{}
	for _, width := range []int{100, 47, 24} {
		for _, mode := range []string{"answer", "empty"} {
			rows = append(rows, map[string]any{"mode": mode, "width": width, "height": 35})
		}
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-btw.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_BTW_SOURCE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "COLUMNS=100", "LINES=35"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var expected struct {
		Rows []struct {
			Frames []struct {
				Text string
				Key  string
			}
		}
	}
	if err = json.Unmarshal(out, &expected); err != nil || len(expected.Rows) != len(rows) {
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
		response, failure := "ACTUAL_SIDE_RESPONSE", ""
		if row["mode"] == "empty" {
			response = ""
			failure = "No response received"
		}
		value, _ := json.Marshal(map[string]any{"question": "owned side question", "response": response, "error": failure})
		panel := &localPanelState{props: LocalPanel{Kind: "btw", Presentation: value}}
		actual := normalize(strings.Join(panel.terminalBtwLines(row["width"].(int), 35, "dark", time.Now()), "\n"))
		want := normalize(expected.Rows[i].Frames[0].Text)
		if actual != want {
			t.Errorf("Actual Source BTW width%d mode%s\nGo:%q\nSource:%q", row["width"], row["mode"], actual, want)
		}
	}
}

func TestTerminalBtwOriginalMountedMarkdownAnswerAndNarrowTable(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Actual pinned Source required")
	}
	rows := []map[string]any{}
	for _, width := range []int{100, 47, 24} {
		for _, response := range terminalMarkdownSourceCorpus() {
			rows = append(rows, map[string]any{"mode": "answer", "width": width, "height": 35, "response": response})
		}
	}
	long := ""
	for i := 0; i < 25; i++ {
		long += "BTW_SIDE_ANSWER_LINE_" + string(rune('A'+i)) + "\n"
	}
	rows = append(rows, map[string]any{"mode": "answer", "width": 100, "height": 35, "response": long, "keys": []string{"\x1b[A", "\x1b[B"}})
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-btw.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_BTW_SOURCE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "COLUMNS=100", "LINES=35"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var expected struct {
		Rows []struct {
			Frames []struct {
				Text string
				Key  string
			}
		}
	}
	if err = json.Unmarshal(out, &expected); err != nil || len(expected.Rows) != len(rows) {
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
		value, _ := json.Marshal(map[string]any{"question": "owned side question", "response": row["response"], "error": ""})
		panel := &localPanelState{props: LocalPanel{Kind: "btw", Presentation: value}}
		for j, frame := range expected.Rows[i].Frames {
			if j > 0 {
				state := NewState(nil, row["width"].(int), 35)
				state.LocalPanel = panel
				key := "up"
				if frame.Key == "\x1b[B" {
					key = "down"
				}
				state.handleTerminalBtwKey(Key{Name: key})
			}
			actual := normalize(strings.Join(panel.terminalBtwLines(row["width"].(int), 35, "dark", time.Now()), "\n"))
			want := normalize(frame.Text)
			if actual != want {
				t.Errorf("Original BTW Markdown row%d frame%d width%d\nGo:%q\nSource:%q", i, j, row["width"], actual, want)
			}
		}
	}
}
