package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestPromptHelpOriginalChatHandlersAndTextInputPreserveActualHelpLifetime(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source required")
	}
	type profile struct {
		name     string
		wire     []string
		keys     []Key
		bindings json.RawMessage
		stash    string
	}
	question := Key{Name: "?", Text: "?"}
	profiles := []profile{
		{"undo-empty", []string{"?", "\x1f"}, []Key{question, {Name: "ctrl+_"}}, nil, ""},
		{"stash-empty", []string{"?", "\x13"}, []Key{question, {Name: "ctrl+s"}}, nil, ""},
		{"empty-clipboard", []string{"?", "\x16"}, []Key{question, {Name: "ctrl+v"}}, nil, ""},
		{"custom-newline", []string{"?", "\x1bn"}, []Key{question, {Name: "alt+n"}}, json.RawMessage(`[{"context":"Chat","bindings":{"alt+n":"chat:newline"}}]`), ""},
		{"typed-text", []string{"?", "a"}, []Key{question, {Name: "a", Text: "a"}}, nil, ""},
		{"help-dismiss", []string{"?", "\x1b"}, []Key{question, {Name: "esc"}}, nil, ""},
		{"raw-return", []string{"?", "\r"}, []Key{question, {Name: "enter"}}, nil, ""},
		{"model-picker", []string{"?", "\x1bp"}, []Key{question, {Name: "alt+p"}}, nil, ""},
		{"thinking-picker", []string{"?", "\x1bt"}, []Key{question, {Name: "alt+t"}}, nil, ""},
		{"double-question", []string{"?", "?"}, []Key{question, question}, nil, ""},
		{"empty-backspace", []string{"?", "\x7f"}, []Key{question, {Name: "backspace"}}, nil, ""},
		{"unbound-undo", []string{"?", "\x1f"}, []Key{question, {Name: "ctrl+_"}}, json.RawMessage(`[{"context":"Chat","bindings":{"ctrl+_":null}}]`), ""},
		{"empty-kill-to-start", []string{"?", "\x15"}, []Key{question, {Name: "ctrl+u"}}, nil, ""},
		{"printable-chat-binding", []string{"?", "z"}, []Key{question, {Text: "z"}}, json.RawMessage(`[{"context":"Chat","bindings":{"z":"chat:undo"}}]`), ""},
		{"printable-explicit-unbound", []string{"?", "z"}, []Key{question, {Text: "z"}}, json.RawMessage(`[{"context":"Chat","bindings":{"z":null}}]`), ""},
		{"stash-restore-keeps-help", []string{"?", "\x13", "a"}, []Key{question, {Name: "ctrl+s"}, {Text: "a"}}, nil, "stored"},
		{"newline-whitespace-stash", []string{"?", "\x1bn", "\x13"}, []Key{question, {Name: "alt+n"}, {Name: "ctrl+s"}}, json.RawMessage(`[{"context":"Chat","bindings":{"alt+n":"chat:newline"}}]`), ""},
		{"vim-stash-external-input-round-trip", []string{"?", "\x13", "\x13", "/", "h", "e", "l", "p"}, []Key{question, {Name: "ctrl+s"}, {Name: "ctrl+s"}, {Text: "/"}, {Text: "h"}, {Text: "e"}, {Text: "l"}, {Text: "p"}}, nil, "/he"},
	}
	rows := []map[string]any{}
	for _, p := range profiles {
		row := map[string]any{"keys": p.wire}
		if p.bindings != nil {
			row["bindings"] = p.bindings
		}
		if p.stash != "" {
			row["stash"] = map[string]any{"text": p.stash, "cursorOffset": len(p.stash), "pastedContents": map[string]any{}}
		}
		if p.name == "vim-stash-external-input-round-trip" {
			row["vim"] = true
		}
		rows = append(rows, row)
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-help-input-keys.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_HELP_INPUT_KEYS_SOURCE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=100", "LINES=80", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatal(err, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatal("Original help keys wrote unexpected stderr", stderr.String())
	}
	var expected struct {
		Results [][]struct {
			HelpOpen bool
			Input    string
		}
		Blocked []json.RawMessage
	}
	if err := json.Unmarshal(stdout.Bytes(), &expected); err != nil || len(expected.Results) != len(profiles) || len(expected.Blocked) > 0 {
		t.Fatal("Original help key receipt invalid", err, stdout.String())
	}
	for index, p := range profiles {
		t.Run(p.name, func(t *testing.T) {
			s := NewState(nil, 100, 80)
			s.stash, s.stashMode = p.stash, "prompt"
			if p.name == "vim-stash-external-input-round-trip" {
				s.Preferences.EditorMode = "vim"
			}
			if p.bindings != nil {
				bindings, err := ParseBindingBlocks(p.bindings)
				if err != nil {
					t.Fatal(err)
				}
				s.SetKeybindings(append(s.bindings, bindings...))
			}
			if len(expected.Results[index]) != len(p.keys) {
				t.Fatal("Original key steps differ")
			}
			for step, key := range p.keys {
				s.HandleKey(key, time.Unix(100, 0).Add(time.Duration(step)*time.Second))
				want := expected.Results[index][step]
				if s.HelpOpen != want.HelpOpen || s.Editor.Text() != want.Input {
					t.Fatalf("Actual key%d %+v: Source help=%v input=%q; Go help=%v input=%q", step, key, want.HelpOpen, want.Input, s.HelpOpen, s.Editor.Text())
				}
			}
		})
	}
}
