package tui

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestActualSourceMountedPluginOptionsKeysSequentialFieldsRawUTF16AndPayload(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Ink required")
	}
	rows := []struct {
		keys    []string
		initial string
	}{{[]string{"x", "\r", "xy", "\t", "\r"}, "seed"}, {[]string{"y", "n", "\r"}, "seed"}, {[]string{"😀", "\x7f", "\t", "\r"}, ""}, {[]string{"😀", "\x1b[3~", "\t", "\r"}, ""}, {[]string{"\x01", "\x0b", "X", "\t", "TOKEN", "\r"}, "seed"}, {[]string{"\x1b[A", "\x1b[B", "\x1b[D", "\x1b[C", "\t", "\t", "\r"}, "seed"}, {[]string{"\x1b[200~Pasted 😀\nline\x1b[201~", "\t", "\r"}, "seed"}}
	inputRows := []map[string]any{}
	for _, row := range rows {
		inputRows = append(inputRows, map[string]any{"schema": map[string]any{"word": map[string]any{"type": "string", "title": "Word"}, "token": map[string]any{"type": "string", "title": "Token", "sensitive": true}}, "initial": map[string]string{"word": row.initial}, "keys": row.keys})
	}
	// A stable field order is part of the actual dialog contract.
	for _, row := range inputRows {
		row["schema"] = json.RawMessage(`{"word":{"type":"string","title":"Word"},"token":{"type":"string","title":"Token","sensitive":true}}`)
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-plugin-options-input.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PLUGIN_OPTIONS_INPUT_SOURCE=1", "BUN_OPTIONS=--no-env-file"}
	data, _ := json.Marshal(map[string]any{"rows": inputRows})
	cmd.Stdin = bytes.NewReader(data)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual mounted Options %v %.4000s", err, output)
	}
	var original []struct {
		Frames []struct{ Events []json.RawMessage }
	}
	if err = json.Unmarshal(output, &original); err != nil {
		t.Fatal(err, string(output))
	}
	if len(original) != len(rows) {
		t.Fatal("missing Source profiles")
	}
	for i, row := range rows {
		s := NewState(nil, 100, 35)
		s.Apply(Event{Kind: LocalPanelOpened, LocalPanel: &LocalPanel{ID: "actual-options", Kind: "plugin", PluginOptions: true, Title: "Configure original options", Description: "Plugin options", DismissAction: "plugin-back", Fields: []LocalPanelField{{Name: "word", Label: "Word", Value: row.initial}, {Name: "token", Label: "Token", Sensitive: true}}}})
		events := []map[string]any{}
		for j, value := range row.keys {
			name, text := value, value
			switch value {
			case "\r":
				name, text = "enter", ""
			case "\t":
				name, text = "tab", ""
			case "\x7f":
				name, text = "backspace", ""
			case "\x1b[3~":
				name, text = "delete", ""
			case "\x01":
				name, text = "ctrl+a", ""
			case "\x0b":
				name, text = "ctrl+k", ""
			case "\x1b[A":
				name, text = "up", ""
			case "\x1b[B":
				name, text = "down", ""
			case "\x1b[D":
				name, text = "left", ""
			case "\x1b[C":
				name, text = "right", ""
			}
			var actions []action
			if strings.HasPrefix(value, "\x1b[200~") {
				s.Paste(strings.TrimSuffix(strings.TrimPrefix(value, "\x1b[200~"), "\x1b[201~"))
			} else {
				actions = s.HandleKey(Key{Name: name, Text: text}, time.Now())
			}
			for _, action := range actions {
				if action.kind != actionLocalPanel {
					t.Fatal("options input escaped native panel", action)
				}
				switch action.panel.Action {
				case "plugin-back":
					events = append(events, map[string]any{"op": "cancel"})
				case "save":
					events = append(events, map[string]any{"op": "save", "values": action.panel.FieldsJSON})
				default:
					t.Fatal("unknown options action", action)
				}
			}
			expected := []json.RawMessage{}
			for _, event := range original[i].Frames[j+1].Events {
				var envelope map[string]json.RawMessage
				_ = json.Unmarshal(event, &envelope)
				if envelope["op"] != nil {
					expected = append(expected, event)
				}
			}
			var a, b any
			actual, _ := json.Marshal(events)
			wanted, _ := json.Marshal(expected)
			_ = json.Unmarshal(actual, &a)
			_ = json.Unmarshal(wanted, &b)
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("actual Source options row%d key%d %q differs: %s Source%s", i, j, value, actual, wanted)
			}
		}
	}
}
