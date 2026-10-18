package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/charmbracelet/x/ansi"
)

type prFooterHookSession struct {
	testSession
	fetches int
}

func (s *prFooterHookSession) FetchTerminalPRStatus(context.Context) (json.RawMessage, error) {
	s.fetches++
	return json.RawMessage("null"), nil
}

func terminalPRFooterOriginal(t *testing.T, input any) protocol.JSONObject {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual pinned Source Bun required")
	}
	home := t.TempDir()
	value := presentationObject(mustPRJSON(input))
	value.Set("home", mustPRJSON(home))
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-pr-footer.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_PR_SOURCE=1"}
	cmd.Stdin = bytes.NewReader(mustPRJSON(value))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual original PR hook %v %.5000s", err, output)
	}
	var parsed protocol.JSONObject
	if err = parsed.UnmarshalJSON(output); err != nil {
		t.Fatal(err, string(output))
	}
	return parsed
}
func mustPRJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

type prHookStep struct {
	At    int64  `json:"at"`
	Op    string `json:"op"`
	Value any    `json:"value,omitempty"`
	Index *int   `json:"index,omitempty"`
}
type prHookScenario struct {
	Start int64        `json:"start"`
	Steps []prHookStep `json:"steps"`
}
type prHookTrace struct {
	State    protocol.JSONObject `json:"state"`
	Fetches  []int64             `json:"fetches"`
	Timers   []int64             `json:"timers"`
	Disabled bool                `json:"disabled"`
}

func TestTerminalPRFooterPollingMatchesOriginalHookLateCompletionIdleSlowDisableAndTurnRestart(t *testing.T) {
	const start int64 = 1700000000000
	pr := func(url string) any { return map[string]any{"number": 812, "url": url, "reviewState": "approved"} }
	step := func(at int64, op string, value any) prHookStep {
		return prHookStep{At: start + at, Op: op, Value: value}
	}
	index := func(v int) *int { return &v }
	rows := []prHookScenario{
		{Start: start, Steps: []prHookStep{step(1000, "resolve", pr("old")), step(60000, "tick", nil), step(61000, "tick", nil), step(62000, "resolve", pr("changed-url-only")), step(122000, "tick", nil), step(123000, "resolve", nil)}},
		{Start: start, Steps: []prHookStep{step(500, "loading", true), {At: start + 600, Op: "resolve", Value: pr("cancelled"), Index: index(0)}, step(700, "resolve", pr("current")), step(1000, "loading", false), step(60499, "tick", nil), step(60500, "tick", nil), step(61000, "resolve", nil)}},
		{Start: start, Steps: []prHookStep{step(4000, "resolve", pr("threshold")), step(64000, "tick", nil), step(68001, "resolve", nil), step(130000, "tick", nil), step(131000, "loading", true), step(132000, "enabled", false), step(133000, "enabled", true), step(134000, "tick", nil)}},
		{Start: start, Steps: []prHookStep{step(200, "enabled", false), step(300, "resolve", pr("ignored")), step(1000, "enabled", true), step(2000, "resolve", pr("visible")), step(3000, "enabled", false), step(4000, "enabled", true), step(60999, "tick", nil), step(61000, "tick", nil), step(62000, "resolve", nil)}},
	}
	idle := prHookScenario{Start: start, Steps: []prHookStep{step(0, "resolve", pr("idle"))}}
	for minute := int64(1); minute <= 61; minute++ {
		idle.Steps = append(idle.Steps, step(minute*60000, "tick", nil))
		if minute < 60 {
			idle.Steps = append(idle.Steps, step(minute*60000, "resolve", pr("idle")))
		}
	}
	idle.Steps = append(idle.Steps, step(62*60000, "interaction", nil), step(62*60000+1000, "tick", nil), step(62*60000+2000, "loading", true), step(62*60000+3000, "resolve", pr("restarted")))
	rows = append(rows, idle)
	output := terminalPRFooterOriginal(t, map[string]any{"polling": rows})
	var expected [][]prHookTrace
	if err := json.Unmarshal(output.Get("polling"), &expected); err != nil || len(expected) != len(rows) {
		t.Fatal("Source hook inventory", err, string(output.Get("polling")))
	}
	for rowIndex, row := range rows {
		t.Run(string(rune('A'+rowIndex)), func(t *testing.T) {
			session := &prFooterHookSession{}
			m := terminalModel{session: session, state: NewState(nil, 90, 30), ctx: context.Background()}
			m.state.Preferences.PRStatusFooterEnabled = true
			pending := []terminalPRFooterResultMsg{}
			fetches := []int64{}
			run := func(command tea.Cmd, now int64) {
				if command != nil {
					msg, ok := command().(terminalPRFooterResultMsg)
					if !ok {
						t.Fatal("actual command did not return native fetch completion")
					}
					msg.started = time.UnixMilli(now)
					pending = append(pending, msg)
					fetches = append(fetches, now)
				}
			}
			run(m.terminalPRFooterRestart(time.UnixMilli(row.Start)), row.Start)
			for i, action := range row.Steps {
				now := time.UnixMilli(action.At)
				switch action.Op {
				case "resolve":
					at := len(pending) - 1
					if action.Index != nil {
						at = *action.Index
					}
					if at < 0 || at >= len(pending) {
						t.Fatal("actual native pending fetch missing", i, at)
					}
					msg := pending[at]
					msg.completed = now
					msg.value = mustPRJSON(action.Value)
					m.terminalPRFooterResult(msg)
				case "loading":
					if action.Value == true {
						m.state.ActiveTurn = "actual-turn"
					} else {
						m.state.ActiveTurn = ""
					}
					run(m.terminalPRFooterRestart(now), action.At)
				case "enabled":
					m.state.Preferences.PRStatusFooterEnabled = action.Value == true
					run(m.terminalPRFooterRestart(now), action.At)
				case "interaction":
					m.terminalPRFooterInteraction()
				case "tick":
					run(m.terminalPRFooterTick(now), action.At)
				}
				state := m.state.TerminalPRStatus.Clone()
				for _, key := range []string{"number", "url", "reviewState"} {
					if len(state.Get(key)) == 0 {
						state.Set(key, json.RawMessage("null"))
					}
				}
				if len(state.Get("lastUpdated")) == 0 {
					state.Set("lastUpdated", json.RawMessage("0"))
				}
				timers := []int64{}
				if !m.terminalPRFooter.nextPoll.IsZero() {
					timers = append(timers, m.terminalPRFooter.nextPoll.UnixMilli())
				}
				actual := prHookTrace{State: state, Fetches: append([]int64{}, fetches...), Timers: timers, Disabled: m.terminalPRFooter.disabled}
				var got, want any
				_ = json.Unmarshal(mustPRJSON(actual), &got)
				_ = json.Unmarshal(mustPRJSON(expected[rowIndex][i]), &want)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("step %d %s Go=%s Source=%s", i, action.Op, mustPRJSON(actual), mustPRJSON(expected[rowIndex][i]))
				}
			}
			if session.fetches != len(fetches) {
				t.Fatal("native command observer count differs")
			}
		})
	}
}

func TestTerminalPRFooterOriginalVisibilityAndActualRenderedLink(t *testing.T) {
	rows := []any{}
	actual := []bool{}
	for _, enabled := range []bool{false, true} {
		for _, primary := range []int{0, 1, 2, 3} {
			for _, columns := range []int{40, 79, 80, 120} {
				for _, status := range []map[string]any{{"number": 12, "url": "https://example.test/pr/12", "reviewState": "approved"}, {"number": nil, "url": "u", "reviewState": "pending"}, {"number": 12, "url": nil, "reviewState": "pending"}, {"number": 12, "url": "u", "reviewState": nil}, {"number": 0, "url": "", "reviewState": "draft"}} {
					rows = append(rows, map[string]any{"enabled": enabled, "status": status, "primary": primary, "columns": columns})
					actual = append(actual, terminalPRStatusVisible(enabled, mustPRJSON(status["number"]), mustPRJSON(status["reviewState"]), mustPRJSON(status["url"]), primary, columns))
				}
			}
		}
	}
	var want []bool
	output := terminalPRFooterOriginal(t, map[string]any{"visibility": rows})
	if err := json.Unmarshal(output.Get("visibility"), &want); err != nil || !reflect.DeepEqual(actual, want) {
		t.Fatal("320 actual Source visibility cases", err)
	}
	s := NewState(nil, 80, 30)
	s.Preferences.PRStatusFooterEnabled = true
	s.Preferences.Theme = "dark"
	s.applyTerminalPRStatusAt(mustPRJSON(map[string]any{"number": 812, "url": "https://example.test/pr/812", "reviewState": "approved"}), time.UnixMilli(123))
	lines := strings.Join(s.terminalPRFooterLines(), "\n")
	if !strings.Contains(ansi.Strip(lines), "PR #812") || !strings.Contains(lines, "\x1b]8;;https://example.test/pr/812") || !strings.Contains(lines, "\x1b[38;2;78;186;101m") {
		t.Fatal("actual themed link", lines)
	}
	s.PermissionMode = "plan"
	s.Width = 79
	if len(s.terminalPRFooterLines()) != 0 {
		t.Fatal("narrow mode footer should hide PR")
	}
	s.Width = 80
	if len(s.terminalPRFooterLines()) != 1 {
		t.Fatal("standard 80 column mode should retain PR")
	}
	s.BackgroundTasks = presentationObject([]byte(`{"agent":{"type":"local_agent","status":"running"}}`))
	if len(s.terminalPRFooterLines()) != 0 {
		t.Fatal("two primary items should hide PR")
	}
	s.Preferences.PRStatusFooterEnabled = false
	if len(s.terminalPRFooterLines()) != 0 {
		t.Fatal("saved disabled setting must hide actual result")
	}
}

func TestTerminalPRFooterActualOriginalBadgeStylesAndSixThemeRoles(t *testing.T) {
	rows := []any{}
	for _, review := range []string{"approved", "changes_requested", "pending", "draft", "merged", "closed"} {
		rows = append(rows, map[string]any{"number": 812, "url": "https://example.test/pr/812", "reviewState": review})
	}
	output := terminalPRFooterOriginal(t, map[string]any{"badges": rows})
	var badges []struct {
		Children []json.RawMessage `json:"children"`
	}
	if err := json.Unmarshal(output.Get("badges"), &badges); err != nil || len(badges) != len(rows) {
		t.Fatal("Source actual React badge inventory", err)
	}
	var themes map[string]map[string]string
	if err := json.Unmarshal(output.Get("themeRoles"), &themes); err != nil || len(themes) != 6 {
		t.Fatal("Source actual theme inventory", err)
	}
	for index, badge := range badges {
		prefix := presentationObject(badge.Children[0])
		prefixProps, _ := prefix.Object("props")
		if string(prefixProps.Get("dimColor")) != "true" || prefixProps.Get("color") != nil {
			t.Fatal("original PR prefix is dim default text", prefix)
		}
		link := presentationObject(badge.Children[2])
		var children []protocol.JSONObject
		_ = json.Unmarshal(link.Get("children"), &children)
		props, _ := children[0].Object("props")
		role := props.String("color")
		if string(props.Get("underline")) != "true" || (string(props.Get("dimColor")) == "true") != (role == "") {
			t.Fatal("original PR number link styling", props)
		}
		row := rows[index].(map[string]any)
		for theme, values := range themes {
			s := NewState(nil, 90, 30)
			s.Preferences.PRStatusFooterEnabled, s.Preferences.Theme = true, theme
			s.applyTerminalPRStatusAt(mustPRJSON(row), time.UnixMilli(123))
			lines := s.terminalPRFooterLines()
			if len(lines) != 1 || !strings.HasPrefix(lines[0], "\x1b[2mPR\x1b[22m ") || ansi.Strip(lines[0]) != "PR #812" {
				t.Fatal("actual Source label/prefix", lines)
			}
			if role != "" && !strings.Contains(lines[0], terminalThemeColorEscape(values[role], false)) {
				t.Fatal("actual original theme role", theme, role, lines)
			}
			if role == "" && !strings.Contains(lines[0], "\x1b[2m#812\x1b[22m") {
				t.Fatal("actual draft/closed number dim", lines)
			}
		}
	}
}
