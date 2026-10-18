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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestTerminalFooterOriginalMountedTasksTeamsCompanionOrderAndTransitions(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source required")
	}
	type row struct {
		Keys        []any                 `json:"keys"`
		Tasks       protocol.JSONObject   `json:"tasks"`
		Teams       []protocol.JSONObject `json:"teams,omitempty"`
		Companion   bool                  `json:"companion,omitempty"`
		Expanded    string                `json:"expanded,omitempty"`
		Selected    string                `json:"selected,omitempty"`
		Selecting   bool                  `json:"selecting,omitempty"`
		Draft       string                `json:"draft,omitempty"`
		LastLine    *bool                 `json:"lastLine,omitempty"`
		Bottom      *bool                 `json:"bottom,omitempty"`
		Suggestions int                   `json:"suggestions,omitempty"`
		Bindings    json.RawMessage       `json:"bindings,omitempty"`
	}
	object := func(value string) protocol.JSONObject { return presentationObject([]byte(value)) }
	shell := object(`{"bash":{"id":"bash","type":"local_bash","status":"running"}}`)
	teamTasks := object(`{"b":{"id":"b","type":"in_process_teammate","status":"running","identity":{"agentName":"Beta"}},"a":{"id":"a","type":"in_process_teammate","status":"running","identity":{"agentName":"Alpha"}}}`)
	teams := []protocol.JSONObject{cachedTeam("owned team", 1)}
	f := false
	keys := func(values ...string) []any {
		result := []any{}
		for _, value := range values {
			result = append(result, value)
		}
		return result
	}
	rows := []row{
		{Tasks: shell, Teams: teams, Companion: true, Keys: keys("\x1b[B", "\x1b[C", "\x1b[C", "\x1b[D", "\x1b[A", "\x1b[B")},
		{Tasks: teamTasks, Teams: teams, Companion: true, Keys: keys("\x1b[B", "\x1b[C", "\r", "\x1b[D", "\r", "\x1b[B", "\x1b[C", "x")},
		{Tasks: teamTasks, Teams: teams, Expanded: "teammates", Keys: keys("\x1b[B", "\x1b[D", "\x1b[A", "\x1b[B", "\r")},
		{Tasks: object(`{"bash":{"id":"bash","type":"local_bash","status":"running","isBackgrounded":false}}`), Teams: teams, Keys: keys("\x1b[B", "\r")},
		{Tasks: object(`{"bash":{"id":"bash","type":"local_bash","status":"pending"}}`), Teams: teams, Keys: keys("\x1b[B", "\r")},
		{Tasks: object(`{"a":{"id":"a","type":"in_process_teammate","status":"running","isBackgrounded":false,"identity":{"agentName":"Alpha"}}}`), Teams: teams, Expanded: "teammates", Keys: keys("\x1b[B", "\x1b[B")},
		{Tasks: shell, Teams: teams, Keys: []any{"\x1b[B", map[string]any{"tasks": protocol.NewJSONObject()}, map[string]any{"tasks": shell}, "\x1b[B", "\x1b[C", "\x1b", "\x1b[B"}},
		{Tasks: protocol.NewJSONObject(), Teams: teams, Keys: keys("\x1b[B", "\x0b", "o"), Bindings: json.RawMessage(`[{"context":"Footer","bindings":{"ctrl+k o":"footer:openSelected","enter":null}}]`)},
		{Tasks: shell, Bottom: &f, Keys: keys("\x1b[B")},
		{Tasks: shell, LastLine: &f, Draft: "first\nlast", Keys: keys("\x1b[B")},
		{Tasks: shell, Suggestions: 2, Keys: keys("\x1b[B")},
		{Tasks: teamTasks, Selecting: true, Keys: keys("\x1b[B", "\r")},
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-footer-navigation.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_FOOTER_SOURCE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=100", "LINES=35"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	type observation struct {
		Items               []string `json:"items"`
		Selection           *string  `json:"selection"`
		Index               int      `json:"index"`
		View, Viewed, Draft string
		Opened, Submitted   []string
		Hint                bool
	}
	var expected [][]observation
	if err = json.Unmarshal(out, &expected); err != nil || len(expected) != len(rows) {
		t.Fatal(err, string(out))
	}
	stroke := map[string]Key{"\x1b[B": {Name: "down"}, "\x1b[A": {Name: "up"}, "\x1b[D": {Name: "left"}, "\x1b[C": {Name: "right"}, "\x1b": {Name: "esc"}, "\r": {Name: "enter"}, "\x0b": {Name: "ctrl+k"}, "x": {Name: "x", Text: "x"}, "o": {Name: "o", Text: "o"}}
	for i, row := range rows {
		s := NewState([]Completion{{Name: "buddy", Kind: "local-jsx"}}, 100, 35)
		s.SetTeammateTasks(row.Tasks)
		s.CachedTeams = cloneCachedTeams(row.Teams)
		s.Teammates.ExpandedView = row.Expanded
		if row.Companion {
			s.applyTerminalBuddy([]byte(`{"companion":{"name":"Owned"}}`))
		}
		s.selectTerminalFooter(row.Selected)
		s.Editor.Set(row.Draft)
		if row.LastLine != nil {
			s.Editor.cursor = 0
		}
		if row.Bottom != nil {
			s.historyIndex = 1
		}
		if row.Selecting {
			s.Teammates.ViewSelectionMode = "selecting-agent"
		}
		s.Completions = make([]Completion, row.Suggestions)
		if len(row.Bindings) > 0 {
			custom, e := ParseBindingBlocks(row.Bindings)
			if e != nil {
				t.Fatal(e)
			}
			s.SetKeybindings(append(DefaultKeybindings("macos", false, KeybindingFeatures{}), custom...))
		}
		opened, submitted := []string{}, []string{}
		hint := false
		for j, raw := range row.Keys {
			if change, ok := raw.(map[string]any); ok {
				s.SetTeammateTasks(change["tasks"].(protocol.JSONObject))
			} else {
				draft := s.Editor.Text()
				_, actions := s.handleTerminalFooterKey(stroke[raw.(string)], time.Now())
				for _, a := range actions {
					if a.kind == actionTerminalTeams {
						opened = append(opened, "teams")
					}
					if a.kind == actionInvoke && a.name == "command:tasks" {
						opened = append(opened, "tasks")
					}
					if a.kind == actionInvoke && a.name == "app:markTasksHint" {
						hint = true
					}
					if a.kind == actionInvoke && strings.HasPrefix(a.name, "command:panel:buddy\x00") {
						submitted = append(submitted, "/buddy")
						s.Editor.Set(draft)
					}
				}
			}
			selection := s.selectedTerminalFooter()
			var selected *string
			if selection != "" {
				selected = &selection
			}
			actual := observation{s.terminalFooterItems(), selected, s.footerTeammateIndex, s.Teammates.ViewSelectionMode, s.Teammates.ViewingAgentTaskID, s.Editor.Text(), opened, submitted, hint}
			if !reflect.DeepEqual(actual, expected[i][j]) {
				t.Fatalf("Original mounted Footer row%d key%d Go%+v Source%+v", i, j, actual, expected[i][j])
			}
		}
	}
}
