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

func TestActualSourceMountedShellPermissionDebugKeysAndHint(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual whole Source shell/Ink/keybinding consumer required")
	}
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-permission-debug-keys.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_PERMISSION_DEBUG_SOURCE=1", "BUN_OPTIONS=--no-env-file", "PATH=" + filepath.Join(home, "bin"), "DISABLE_TELEMETRY=1", "CLAUDE_CODE_ESSENTIAL=1", "ANTHROPIC_API_KEY=fake-permission-debug-only", "ANTHROPIC_BASE_URL=http://127.0.0.1:9"}
	inputRows := []map[string]any{}
	for _, tool := range []string{"Bash", "PowerShell"} {
		for _, hint := range []bool{false, true} {
			inputRows = append(inputRows, map[string]any{"tool": tool, "debug": hint})
		}
	}
	input, _ := json.Marshal(map[string]any{"rows": inputRows})
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatal("actual whole Source mounted permission", err, stderr.String())
	}
	var original []struct {
		Tool   string
		Debug  bool
		Frames []struct {
			Key, Output string
			Events      []struct{ Op string }
			Handlers    []string
		}
	}
	if err = json.Unmarshal(output, &original); err != nil || len(original) != 4 {
		t.Fatal("actual mounted Source receipts", err, len(original))
	}
	for index, source := range original {
		if len(source.Frames) != 11 {
			t.Fatal("actual key sequence did not settle", source.Tool, source.Debug, len(source.Frames))
		}
		mount, shown, hidden := ansi.Strip(source.Frames[0].Output), ansi.Strip(source.Frames[1].Output), ansi.Strip(source.Frames[9].Output)
		if !strings.Contains(mount, "Do you want to proceed?") || !strings.Contains(shown, "Owned reason") || strings.Contains(shown, "Do you want to proceed?") || !strings.Contains(hidden, "Do you want to proceed?") {
			t.Fatal("actual Select/debug mount transition", source.Tool, mount, shown, hidden)
		}
		if strings.Contains(mount, "Ctrl+d to show debug info") != source.Debug || strings.Contains(shown, "Ctrl-D to hide debug info") != source.Debug {
			t.Fatal("actual debug hint predicate differs", source.Tool, source.Debug, mount, shown)
		}
		for _, frame := range source.Frames[:10] {
			for _, event := range frame.Events {
				if event.Op == "allow" || event.Op == "reject" || event.Op == "abort" || event.Op == "done" {
					t.Fatal("unmounted Source Select consumed a permission key", source.Tool, frame.Key, event.Op)
				}
			}
		}
		allows, done := 0, 0
		for _, event := range source.Frames[10].Events {
			if event.Op == "allow" {
				allows++
			}
			if event.Op == "done" {
				done++
			}
		}
		if allows != 1 || done != 1 {
			t.Fatal("remounted actual Select did not consume Enter exactly once", source.Tool, allows, done)
		}
		// The same action sequence reaches the actual Go State consumer. Its
		// ordinary application/PTY producer is exercised separately.
		state := NewState(nil, 100, 35)
		state.Editor.Set("actual pending command")
		turn := key(state, "enter")[0].input.TurnID
		request := PermissionRequest{ID: source.Tool, ToolName: source.Tool, Debug: &PermissionDebugData{DebugHint: source.Debug, Result: json.RawMessage(`{"behavior":"ask","message":"Owned decision","decisionReason":{"type":"other","reason":"Owned reason"}}`), Context: json.RawMessage(`{}`)}, Choices: []PermissionChoice{{Key: "y", Label: "Yes", Decision: PermissionDecision{Allowed: true}}, {Key: "n", Label: "No", Decision: PermissionDecision{}}}}
		state.Apply(Event{TurnID: turn, Kind: PermissionAsked, Permission: &request})
		for _, name := range []string{"ctrl+d", "enter", "y", "n", "esc", "up", "down", "tab"} {
			if actions := key(state, name); len(actions) != 0 || !state.PermissionDebugVisible(request) || len(state.Permissions) != 1 {
				t.Fatal("native mounted key sequence differs", index, name, actions)
			}
		}
		if actions := key(state, "ctrl+d"); len(actions) != 0 || state.PermissionDebugVisible(request) {
			t.Fatal("native toggle did not remount choices", actions)
		}
		if actions := key(state, "enter"); len(actions) != 1 || actions[0].kind != actionPermission || !actions[0].decision.Allowed {
			t.Fatal("native remounted approval differs", actions)
		}
	}
}
