package application

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualSourceKeybindingTrustedGateFileParsingAndConflictPrecedence(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual frozen Source Bun required")
	}
	contents := []*string{nil}
	for _, content := range []string{
		`{}`, `[]`, `{"bindings":null}`, `{"bindings":[null]}`, `{"bindings":[{"context":"Chat","bindings":[]}]}`,
		`{"future":{"preserve":17},"bindings":[{"context":"Chat","bindings":{"ctrl+t":null,"ctrl+k ctrl+p":"chat:modelPicker","ctrl+p":"command:help"}}]}`,
		`{"bindings":[{"context":"Chat","bindings":{"ctrl+k":"command:help","ctrl+k":"command:tasks"}},{"context":"Chat","bindings":{"control+k":null}}]}`,
		`{"bindings":[{"context":"Unknown","bindings":{"ctrl++k":"command:bad name"}},{"context":"Global","bindings":{"alt+x":"command:help"}}]}`,
		`{"bindings":[{"context":"Chat","bindings":{"ctrl+c":"chat:modelPicker","cmd+c":null,"a":"voice:pushToTalk","alt+j":99}}]}`,
		`{"bindings":[{"context":"Chat","bindings":{"":null,"super":"command:help"}}]}`,
	} {
		copy := content
		contents = append(contents, &copy)
	}
	// Separate fields retain the exact Source synchronous/async input choices.
	var rows []map[string]any
	for _, enabled := range []bool{false, true} {
		for _, synchronous := range []bool{false, true} {
			for _, content := range contents {
				rows = append(rows, map[string]any{"enabled": enabled, "synchronous": synchronous, "content": content})
			}
		}
	}
	home := t.TempDir()
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-keybindings-loader.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_KEYBINDING_LOADER_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "USER_TYPE=external", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "platform": "macos", "rows": rows}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal("actual Source loader failed", err, string(output))
	}
	type sourceBinding struct {
		Chord   []tui.Keystroke `json:"chord"`
		Action  json.RawMessage `json:"action"`
		Context string          `json:"context"`
	}
	var source struct {
		Results []struct {
			Bindings       []sourceBinding
			Warnings       []terminalKeybindingWarning
			Reads, Watched int
			GateCalls      []struct {
				Name     string
				Fallback bool
			}
		}
		Capabilities struct{ Blocked []string }
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source.Results) != len(rows) || len(source.Capabilities.Blocked) != 0 {
		t.Fatal("invalid actual Source result", err, string(output))
	}
	for i, row := range rows {
		enabled, synchronous := row["enabled"].(bool), row["synchronous"].(bool)
		content := row["content"].(*string)
		reads := 0
		state := &terminalKeybindingsState{platform: "macos", enabled: func() bool { return enabled }, readFile: func(string) ([]byte, error) {
			reads++
			if content == nil {
				return nil, os.ErrNotExist
			}
			return []byte(*content), nil
		}}
		got := state.load(synchronous)
		want := source.Results[i]
		var expected []tui.Keybinding
		for _, binding := range want.Bindings {
			var action *string
			if string(binding.Action) != "null" {
				var value string
				if json.Unmarshal(binding.Action, &value) != nil {
					value = string(binding.Action)
				}
				action = &value
			}
			expected = append(expected, tui.Keybinding{Chord: binding.Chord, Action: action, Context: binding.Context})
		}
		if !reflect.DeepEqual(got.bindings, expected) || !reflect.DeepEqual(got.warnings, want.Warnings) {
			t.Fatalf("row %d gate=%t sync=%t content=%v mismatched: Go warnings=%s Source warnings=%s; bindings %d/%d", i, enabled, synchronous, content, raw(got.warnings), raw(want.Warnings), len(got.bindings), len(expected))
		}
		if reads != want.Reads || !enabled && (reads != 0 || want.Watched != 0) || enabled && want.Watched != 1 {
			t.Fatal("actual trusted gate did not own file reads and watches", i, reads, want.Reads, want.Watched)
		}
		for _, gate := range want.GateCalls {
			if gate.Name != "tengu_keybinding_customization_release" || gate.Fallback {
				t.Fatal("Source availability boundary changed", gate)
			}
		}
	}
}

func TestActualSourceKeybindingPhysicalWatcherAtomicReloadDeleteAndDispose(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual frozen Source Bun required")
	}
	home := t.TempDir()
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-keybindings-loader.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	initial := `{"future":17,"bindings":[{"context":"Chat","bindings":{"alt+p":null}}]}`
	updated := `{"future":19,"bindings":[{"context":"Chat","bindings":{"alt+j":"command:skills"}}]}`
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_KEYBINDING_LOADER_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "USER_TYPE=external", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "platform": "macos", "rows": []any{map[string]any{"enabled": true, "synchronous": true, "content": initial, "changes": []any{updated, nil}}}}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal("actual Source physical watcher failed", err, string(output))
	}
	var source struct {
		Results []struct {
			Changes []struct {
				Bindings []tui.Keybinding
				Warnings []terminalKeybindingWarning
			}
			Reads, Watched int
		}
		Capabilities struct{ Blocked []string }
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source.Results) != 1 || len(source.Results[0].Changes) != 2 || len(source.Capabilities.Blocked) != 0 {
		t.Fatal("invalid actual watcher evidence", err, string(output))
	}
	row := source.Results[0]
	if terminalBindingAction(row.Changes[0].Bindings, "alt+j").Action != "command:skills" || !reflect.DeepEqual(row.Changes[1].Bindings, tui.DefaultKeybindings("macos", true, tui.KeybindingFeatures{})) || row.Reads != 2 || row.Watched != 1 {
		t.Fatal("original stable reload/delete lifecycle differed", row.Reads, row.Watched)
	}
}
