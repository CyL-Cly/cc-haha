package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
)

func TestExternalEditorReadsExactBytesTrimsOneNewlineAndCleansTemporaryFile(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"修改中文👋\n", "两行\n\n", "exact"} {
		t.Run(fmt.Sprintf("%q", value), func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			editor := newEditorExecution(ctx, ExternalEditor{Command: []string{binary, "-test.run=^TestExternalEditorFixtureProcess$", "-test.count=1"}, TempDir: dir, Environment: []string{"HOME=" + dir, "USERPROFILE=" + dir, "TUI_EDITOR_HELPER=1", "TUI_EDITOR_CONTENT=" + value}}, "original", strings.NewReader(""), io.Discard)
			if err = editor.Run(); err != nil {
				t.Fatal(err)
			}
			expected := value
			if strings.HasSuffix(value, "\n") && !strings.HasSuffix(value, "\n\n") {
				expected = strings.TrimSuffix(value, "\n")
			}
			if editor.text != expected || !editor.changed {
				t.Fatalf("edited=%q changed=%v", editor.text, editor.changed)
			}
			files, err := os.ReadDir(dir)
			if err != nil || len(files) != 0 {
				t.Fatalf("editor did not clean private temp file: %v %v", files, err)
			}
		})
	}
}

func TestExternalEditorFailureLeavesOriginalPromptAndCleansTemporaryFile(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	editor := newEditorExecution(context.Background(), ExternalEditor{Command: []string{binary, "-test.run=^TestExternalEditorFixtureProcess$", "-test.count=1"}, TempDir: dir, Environment: []string{"HOME=" + dir, "TUI_EDITOR_HELPER=1", "TUI_EDITOR_FAILURE=1"}}, "original 中文", nil, io.Discard)
	if err = editor.Run(); err == nil {
		t.Fatal("external editor failure was hidden")
	}
	if editor.text != "original 中文" || editor.changed {
		t.Fatal("failed editor replaced original prompt")
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 0 {
		t.Fatal("failed editor did not clean temp file")
	}
}

func TestExternalEditorChordAndModalInputFence(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.Editor.Set("original 中文")
	now := time.Now()
	if actions := s.HandleKey(Key{Name: "ctrl+x"}, now); len(actions) != 0 || s.Editing {
		t.Fatal("chord prefix launched editor")
	}
	actions := s.HandleKey(Key{Name: "ctrl+e"}, now.Add(100*time.Millisecond))
	if len(actions) != 1 || actions[0].kind != actionEditor || actions[0].text != "original 中文" || !s.Editing {
		t.Fatal("original editor chord was not handled")
	}
	s.HandleKey(Key{Name: "enter"}, now)
	s.Paste("discard")
	if s.Editor.Text() != "original 中文" || s.Busy() {
		t.Fatal("editor handoff accepted concurrent terminal input")
	}
	if len(s.EOF()) != 1 || !s.Closing {
		t.Fatal("editor fence swallowed terminal EOF")
	}
}

func TestExternalEditorExpiredChordAndPermissionPriority(t *testing.T) {
	s := NewState(nil, 80, 24)
	s.Editor.Set("original")
	s.Editor.Home()
	now := time.Now()
	s.HandleKey(Key{Name: "ctrl+x"}, now)
	s.HandleKey(Key{Name: "ctrl+e"}, now.Add(1100*time.Millisecond))
	if s.Editing || s.Editor.Cursor() != len("original") {
		t.Fatal("expired chord did not fall through to normal readline")
	}
	s.HandleKey(Key{Name: "ctrl+x"}, now)
	s.Permissions = []PermissionRequest{{ID: "permission", ToolName: "Write"}}
	if actions := s.HandleKey(Key{Name: "ctrl+e"}, now); len(actions) != 0 || s.Editing {
		t.Fatal("external editor bypassed permission context")
	}
}

func TestExternalEditorFixtureProcess(t *testing.T) {
	if os.Getenv("TUI_EDITOR_HELPER") != "1" {
		t.Skip("subprocess-only external editor fixture")
	}
	if os.Getenv("TUI_EDITOR_FAILURE") == "1" {
		t.Fatal("fixture editor failed")
	}
	path := os.Args[len(os.Args)-1]
	home := os.Getenv("HOME")
	if filepath.Dir(path) != home {
		t.Fatal("fixture editor requires its disposable HOME directory")
	}
	if os.Getenv("TUI_EDITOR_TERMINAL") == "1" {
		if !term.IsTerminal(os.Stdin.Fd()) {
			t.Fatal("editor was not given the actual terminal descriptor")
		}
		state, err := term.GetState(os.Stdin.Fd())
		if err != nil {
			t.Fatal(err)
		}
		fixture := fixtureSession{home: home}
		fixture.record(map[string]any{"kind": "editor-terminal-state", "state": fmt.Sprintf("%+v", state)})
	}
	if err := os.WriteFile(path, []byte(os.Getenv("TUI_EDITOR_CONTENT")), 0o600); err != nil {
		t.Fatal(err)
	}
}
