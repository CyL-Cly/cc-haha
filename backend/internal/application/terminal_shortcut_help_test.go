package application

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTerminalShortcutHelpActualGlobalNewlineFastAndSourceMountInputs(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source required")
	}
	for _, profile := range []struct {
		name, terminal       string
		installed, backslash any
		disabled             bool
	}{
		{"plain", "", false, false, true},
		{"installed", "", true, false, true},
		{"backslash", "", false, true, true},
		{"legacy-truthy", "", "true", 1, true},
		{"apple", "Apple_Terminal", false, false, true},
		{"pending-org", "", false, false, false},
	} {
		t.Run(profile.name, func(t *testing.T) {
			ctx := context.Background()
			o := nativeSessionOptions(t, "http://127.0.0.1:1")
			o.Interactive = true
			o.Environment["TERM_PROGRAM"] = profile.terminal
			o.Native.Environment["TERM_PROGRAM"] = profile.terminal
			if !profile.disabled {
				delete(o.Environment, "CLAUDE_CODE_DISABLE_FAST_MODE")
				delete(o.Native.Environment, "CLAUDE_CODE_DISABLE_FAST_MODE")
			}
			config := o.Environment["CLAUDE_CONFIG_DIR"]
			if err := os.MkdirAll(config, 0700); err != nil {
				t.Fatal(err)
			}
			globalPath := filepath.Join(config, ".claude.json")
			if err := os.WriteFile(globalPath, raw(map[string]any{"hasCompletedOnboarding": true, "shiftEnterKeyBindingInstalled": profile.installed, "hasUsedBackslashReturn": profile.backslash, "futureGlobal": map[string]any{"kept": 17}}), 0600); err != nil {
				t.Fatal(err)
			}
			s, err := NewSession(ctx, o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(ctx)
			got, err := s.TerminalShortcutHelp(ctx)
			if err != nil {
				t.Fatal(err)
			}
			home, _ := filepath.EvalSymlinks(t.TempDir())
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-shortcut-help.fixture.ts")
			cmd := exec.Command(bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SHORTCUT_HELP_SOURCE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=100", "LINES=80", "FORCE_COLOR=3", "PATH=/usr/bin:/bin"}
			body, _ := json.Marshal(map[string]any{"home": home, "terminal": profile.terminal, "rows": []any{map[string]any{"width": 100, "theme": "dark", "installed": profile.installed, "backslash": profile.backslash, "disabledFast": profile.disabled, "nativeRows": []string{}}}})
			cmd.Stdin = bytes.NewReader(body)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatal(err, string(output))
			}
			var expected []struct{ Metadata tui.TerminalShortcutHelp }
			if json.Unmarshal(output, &expected) != nil || len(expected) != 1 {
				t.Fatal(string(output))
			}
			if got != expected[0].Metadata {
				t.Fatalf("actual Source mount inputs differ: Source%+v Go%+v", expected[0].Metadata, got)
			}
			if err = s.process.Global.Save(func(value *protocol.JSONObject) (*protocol.JSONObject, error) {
				next := value.Clone()
				next.Set("shiftEnterKeyBindingInstalled", []byte("true"))
				return &next, nil
			}); err != nil {
				t.Fatal(err)
			}
			updated, err := s.TerminalShortcutHelp(ctx)
			if err != nil || updated.Newline != "shift + ⏎ for newline" {
				t.Fatal("Actual next mount did not read updated global setting", updated, err)
			}
			data, err := os.ReadFile(globalPath)
			if err != nil || !bytes.Contains(data, []byte(`"futureGlobal"`)) {
				t.Fatal("Unknown global field lost", err, string(data))
			}
		})
	}
}
