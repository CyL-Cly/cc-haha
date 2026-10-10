package cliauth

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"unicode/utf16"

	tea "charm.land/bubbletea/v2"
)

func TestSetupTokenGraphemeEditingAndMaskMatchActualSourceCursor(t *testing.T) {
	samples := []struct {
		text    string
		columns int
		methods []string
		keys    []tea.KeyPressMsg
	}{
		{"authorization-code#state", 80, nil, nil},
		{"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ#STATE", 12, nil, nil},
		{"e\u0301👨‍👩‍👧‍👦中文#state", 80, []string{"left", "backspace"}, []tea.KeyPressMsg{{Code: tea.KeyLeft}, {Code: tea.KeyBackspace}}},
		{"codeX#state", 80, []string{"startOfLine", "right", "right", "right", "right", "del"}, []tea.KeyPressMsg{{Code: tea.KeyHome}, {Code: tea.KeyRight}, {Code: tea.KeyRight}, {Code: tea.KeyRight}, {Code: tea.KeyRight}, {Code: tea.KeyDelete}}},
		{"simple-code#state", 80, []string{"backspace", "backspace"}, []tea.KeyPressMsg{{Code: tea.KeyBackspace}, {Code: tea.KeyBackspace}}},
		{"line one\nline two", 80, []string{"deleteToLineStart"}, []tea.KeyPressMsg{{Code: 'u', Mod: tea.ModCtrl}}},
		{"state\n", 80, []string{"backspace"}, []tea.KeyPressMsg{{Code: tea.KeyBackspace}}},
	}
	var edits []map[string]any
	for _, sample := range samples {
		edits = append(edits, map[string]any{"text": sample.text, "columns": sample.columns, "methods": sample.methods})
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	home := filepath.Join(root, "home")
	os.Mkdir(home, 0700)
	fixture, _ := filepath.Abs("../../testdata/native-setup-token-input.fixture.ts")
	raw, _ := json.Marshal(map[string]any{"env": map[string]string{"SSH_CONNECTION": "owned"}, "edits": edits})
	command := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	command.Dir = home
	command.Stdin = bytes.NewReader(raw)
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + root, "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "NODE_ENV=test", "DISABLE_TELEMETRY=1"}
	out, e := command.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var source struct {
		Edits []struct {
			Text, Masked string
			Offset       int
		}
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Edits) != len(samples) {
		t.Fatal(e, string(out))
	}
	for i, sample := range samples {
		m := &setupModel{columns: sample.columns}
		m.editor.Set(sample.text)
		for _, key := range sample.keys {
			m.edit(key)
		}
		// Cursor.fromText accounts for one terminal cell reserved to the cursor.
		gotMask := setupTokenMasked(m.editor.Text(), sample.columns-1)
		gotOffset := len(utf16.Encode([]rune(m.editor.Text()[:m.editor.Cursor()])))
		if m.editor.Text() != source.Edits[i].Text || gotOffset != source.Edits[i].Offset || gotMask != source.Edits[i].Masked {
			t.Fatalf("sample%d actualSource=%#v go={%q %d %q}", i, source.Edits[i], m.editor.Text(), gotOffset, gotMask)
		}
	}
}
