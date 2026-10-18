package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/charmbracelet/x/ansi"
	"strings"
)

func TestTerminalHelpV2WholeOriginalCustomListScrollFocusAndReadOnlySelection(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned whole HelpV2 Source required")
	}
	steps := []struct {
		input, name string
		noRender    bool
	}{{"\t", "tab", false}, {"\t", "tab", false}, {"\x1b[B", "down", false}, {"\r", "enter", true}}
	for i := 0; i < 5; i++ {
		steps = append(steps, struct {
			input, name string
			noRender    bool
		}{"\x1b[B", "down", false})
	}
	steps = append(steps, struct {
		input, name string
		noRender    bool
	}{"\t", "tab", true})
	for i := 0; i < 6; i++ {
		steps = append(steps, struct {
			input, name string
			noRender    bool
		}{"\x1b[A", "up", false})
	}
	rows := []map[string]any{}
	for _, width := range []int{100, 47, 24} {
		commands := []protocol.JSONObject{}
		original := []map[string]any{}
		for i := 0; i < 12; i++ {
			name := fmt.Sprintf("owned-%02d", i)
			description := fmt.Sprintf("Actual description of custom command %02d", i)
			commands = append(commands, protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: helpListTextJSON(name)}, protocol.JSONField{Name: "description", Value: helpListTextJSON(description + " (user)")}, protocol.JSONField{Name: "builtin", Value: []byte("false")}))
			original = append(original, map[string]any{"name": name, "description": description, "type": "prompt", "source": "userSettings"})
		}
		s := NewState(nil, width, 36)
		data, _ := json.Marshal(TerminalHelpData{Version: "999.0.0-local", Commands: commands, Shortcuts: TerminalShortcutHelp{Platform: "macos"}})
		s.openLocalPanel(&LocalPanel{ID: "owned-help", Kind: "help", HelpDialog: true, Presentation: data})
		paint := [][]string{s.LocalPanel.terminalHelpLines(width, 36, "dark", s.bindings)}
		root := s.LocalPanel.terminalHelpFlexRoot(width, 36, "dark", s.bindings)
		root.layout(float64(width), 18, true, true, false)
		var project func(*terminalHelpFlex) any
		project = func(node *terminalHelpFlex) any {
			children := []any{}
			for _, child := range node.children {
				children = append(children, project(child))
			}
			return map[string]any{"text": ansi.Strip(node.text), "row": node.row, "w": node.w, "h": node.h, "x": node.x, "y": node.y, "children": children}
		}
		if target, err := filepath.Abs(fmt.Sprintf("../../../artifacts/go-migration-20261013/tui-help-long-list-layout-native-%d.json", width)); err == nil {
			value, _ := json.Marshal(project(root))
			_ = os.WriteFile(target, value, 0600)
		}
		keys := []map[string]any{}
		for _, step := range steps {
			actions := s.HandleKey(Key{Name: step.name}, time.Now())
			if len(actions) != 0 || s.LocalPanel == nil || s.Closing {
				t.Fatal("read-only Help list launched or closed a command", actions)
			}
			keys = append(keys, map[string]any{"input": step.input, "noRender": step.noRender})
			paint = append(paint, s.LocalPanel.terminalHelpLines(width, 36, "dark", s.bindings))
		}
		rows = append(rows, map[string]any{"width": width, "height": 36, "commands": original, "keys": keys, "nativeRows": paint, "captureLayout": true})
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-help-v2.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_HELP_V2_SOURCE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_FAST_MODE=1", "COLUMNS=100", "LINES=80", "FORCE_COLOR=3", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal("original mounted Help navigation", err, string(output))
	}
	var source []struct {
		Snapshots []struct {
			Frame  string
			Cells  json.RawMessage
			Closed []any
		}
		NativeCells []json.RawMessage
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source) != len(rows) {
		t.Fatal(err, string(output))
	}
	if target, err := filepath.Abs("../../../artifacts/go-migration-20261013/tui-help-long-list-layout-original-current.json"); err == nil {
		_ = os.WriteFile(target, output, 0600)
	}
	for i, row := range source {
		for j, frame := range row.Snapshots {
			if len(frame.Closed) != 0 {
				t.Error("original readonly navigation closed Help", i, j)
			}
			if !reflect.DeepEqual(frame.Cells, row.NativeCells[j]) {
				t.Errorf("profile%d step%d original Help list differs Source%q Go%q", i, j, frame.Frame, ansi.Strip(strings.Join(rows[i]["nativeRows"].([][]string)[j], "\n")))
			}
		}
	}
}

func helpListTextJSON(value string) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}
