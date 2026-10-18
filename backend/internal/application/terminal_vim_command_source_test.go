package application

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestTerminalVimCommandActualSourceModesOutputAndInteractiveAdmission(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual pinned Source Bun required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	values := []any{nil, "", "normal", "emacs", "vim", "legacy", false, true, 0, 1, map[string]any{"future": true}}
	cases := []any{}
	for _, value := range values {
		global := map[string]any{"editorMode": value, "futureGlobal": 17}
		if value == nil {
			delete(global, "editorMode")
		}
		cases = append(cases, map[string]any{"global": global})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-vim-command.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_VIM_COMMAND_SOURCE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "cases": cases}))
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actualVimcommandSource %v %.2000s", err, data)
	}
	result := obj(data)
	definition := obj(result.Get("definition"))
	if definition.String("name") != "vim" || definition.String("type") != "local" || string(definition.Get("supportsNonInteractive")) != "false" {
		t.Fatal("actual Source admission", definition)
	}
	var rows []json.RawMessage
	if err = json.Unmarshal(result.Get("rows"), &rows); err != nil || len(rows) != len(values) {
		t.Fatal(err)
	}
	for index, value := range values {
		o := nativeSessionOptions(t, "http://127.0.0.1:1")
		o.Interactive = true
		config := o.Environment["CLAUDE_CONFIG_DIR"]
		_ = os.MkdirAll(config, 0700)
		global := map[string]any{"editorMode": value, "futureGlobal": 17}
		if value == nil {
			delete(global, "editorMode")
		}
		_ = os.WriteFile(filepath.Join(config, ".claude.json"), raw(global), 0600)
		s, err := NewSession(context.Background(), o)
		if err != nil {
			t.Fatal(index, err)
		}
		var output string
		var mode string
		emit := func(event tui.Event) error {
			if event.Kind == tui.LocalCommandOutput {
				output = event.Text
			}
			if event.Preferences != nil {
				mode = event.Preferences.EditorMode
			}
			return nil
		}
		handled, err := s.InvokeTerminalCommand(context.Background(), "vim", "ignored Source arguments", emit)
		row := obj(rows[index])
		want := obj(row.Get("result")).String("value")
		wantedMode := obj(row.Get("global")).String("editorMode")
		if !handled || err != nil || output != want || mode != wantedMode {
			t.Fatalf("Source localvim mode%d got %s %s %v want%s %s", index, mode, output, err, wantedMode, want)
		}
		actual, err := s.process.Global.Get()
		if err != nil || actual.String("editorMode") != wantedMode || string(actual.Get("futureGlobal")) != "17" {
			t.Fatal(index, actual, err)
		}
		if err = s.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if handled, err := s.InvokeTerminalCommand(context.Background(), "vim", "", nil); handled || err != nil {
		t.Fatal("headless consumer admitted", handled, err)
	}
}
