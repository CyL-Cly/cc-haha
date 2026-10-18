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

	"github.com/NanmiCoder/cc-haha/backend/internal/teamnavigation"
)

func TestBuddyOriginalPromptInputFooterMountedBindingsFirstCompanionAndDraft(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source required")
	}
	type row struct {
		Keys        []string        `json:"keys"`
		Focused     bool            `json:"focused,omitempty"`
		Selecting   bool            `json:"selecting,omitempty"`
		Draft       string          `json:"draft,omitempty"`
		LastLine    *bool           `json:"lastLine,omitempty"`
		Bottom      *bool           `json:"bottom,omitempty"`
		Suggestions int             `json:"suggestions,omitempty"`
		Bindings    json.RawMessage `json:"bindings,omitempty"`
		Companion   json.RawMessage `json:"companion"`
	}
	f := false
	rows := []row{
		{Keys: []string{"\x1b[B", "\x1b[D", "\x1b[C", "\x1b[B", "\r"}, Draft: "retained draft"},
		{Keys: []string{"\x1b[B", "\x1b[A", "\x0e", "\x10", "\x1b[B", "\x1b"}},
		{Keys: []string{"\x1b[B", "x", "\x1b[B", "\r"}, Draft: "a"},
		{Keys: []string{"\r", "\x1b[A"}, Focused: true, Selecting: true},
		{Keys: []string{"\x1b[B", "\r"}, Bottom: &f},
		{Keys: []string{"\x1b[B", "\r"}, LastLine: &f, Draft: "first\nlast"},
		{Keys: []string{"\x1b[B"}, Suggestions: 2},
		{Keys: []string{"\x1b[B", "\x0b", "o"}, Bindings: json.RawMessage(`[{"context":"Footer","bindings":{"ctrl+k o":"footer:openSelected","enter":null}}]`)},
	}
	for i := range rows {
		rows[i].Companion = json.RawMessage(`{"name":"Owned footer","hatchedAt":0,"appearanceSeed":"owned-footer"}`)
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-buddy-footer.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_BUDDY_SOURCE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=100", "LINES=35"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	type observation struct {
		Focused   bool     `json:"focused"`
		Draft     string   `json:"draft"`
		Submitted []string `json:"submitted"`
	}
	var expected [][]observation
	if err = json.Unmarshal(out, &expected); err != nil || len(expected) != len(rows) {
		t.Fatal(err, string(out))
	}
	keys := map[string]Key{"\x1b[B": {Name: "down"}, "\x1b[A": {Name: "up"}, "\x1b[D": {Name: "left"}, "\x1b[C": {Name: "right"}, "\x0e": {Name: "ctrl+n"}, "\x10": {Name: "ctrl+p"}, "\r": {Name: "enter"}, "\x1b": {Name: "esc"}, "\x0b": {Name: "ctrl+k"}, "x": {Name: "x", Text: "x"}, "o": {Name: "o", Text: "o"}}
	for i, row := range rows {
		s := NewState([]Completion{{Name: "buddy", Kind: "local-jsx"}}, 100, 35)
		s.applyTerminalBuddy(append(append([]byte(`{"companion":`), row.Companion...), '}'))
		s.buddy.focused = row.Focused
		s.Editor.Set(row.Draft)
		if row.LastLine != nil {
			s.Editor.cursor = 0
		}
		if row.Bottom != nil {
			s.historyIndex = 1
		}
		if row.Selecting {
			team := teamnavigation.New()
			team.ViewSelectionMode = "selecting-agent"
			s.Teammates = &team
		}
		if row.Suggestions > 0 {
			s.Completions = make([]Completion, row.Suggestions)
		}
		if len(row.Bindings) > 0 {
			custom, err := ParseBindingBlocks(row.Bindings)
			if err != nil {
				t.Fatal(err)
			}
			s.SetKeybindings(append(DefaultKeybindings("macos", false, KeybindingFeatures{}), custom...))
		}
		submitted := []string{}
		for j, raw := range row.Keys {
			draft := s.Editor.Text()
			handled, actions := s.handleTerminalBuddyFooterKey(keys[raw], time.Now())
			if handled {
				for _, action := range actions {
					if action.kind == actionInvoke && strings.HasPrefix(action.name, "command:panel:buddy\x00") {
						submitted = append(submitted, "/buddy")
						// Source callback retains the draft; command submit lifecycle
						// clears it after onSubmit in the actual REPL, tested by PTY.
						s.Editor.Set(draft)
					}
				}
			}
			actual := observation{s.buddy.focused, s.Editor.Text(), submitted}
			if !reflect.DeepEqual(actual, expected[i][j]) {
				t.Fatalf("Original mounted footer row%d key%d:%q Go%+v Source%+v", i, j, raw, actual, expected[i][j])
			}
		}
	}
}
