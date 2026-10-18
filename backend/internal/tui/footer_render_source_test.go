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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestTerminalTaskAndTeamPillsOriginalWholeComponentsSixThemesGeometryAndStyles(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source required")
	}
	objects := []protocol.JSONObject{}
	for _, content := range []string{
		`{"a":{"id":"a","type":"local_bash","status":"running"}}`,
		`{"a":{"id":"a","type":"local_bash","status":"pending","kind":"monitor"},"b":{"id":"b","type":"local_bash","status":"running"}}`,
		`{"a":{"id":"a","type":"local_agent","status":"running"}}`,
		`{"a":{"id":"a","type":"local_workflow","status":"running"}}`,
		`{"a":{"id":"a","type":"monitor_mcp","status":"running"}}`,
		`{"a":{"id":"a","type":"dream","status":"running"}}`,
		`{"a":{"id":"a","type":"remote_agent","status":"running"}}`,
		`{"a":{"id":"a","type":"remote_agent","status":"running","isUltraplan":true,"ultraplanPhase":"plan_ready"}}`,
		`{"a":{"id":"a","type":"remote_agent","status":"running","isUltraplan":true,"ultraplanPhase":"needs_input"}}`,
		`{"a":{"id":"a","type":"remote_agent","status":"running","isUltraplan":true}}`,
		`{"a":{"id":"a","type":"local_bash","status":"running","isBackgrounded":false}}`,
		`{"a":{"id":"a","type":"local_bash","status":"completed"}}`,
		`{"a":{"id":"a","type":"local_agent","status":"running"},"b":{"id":"b","type":"local_bash","status":"pending"}}`,
		`{"a":{"id":"a","type":"in_process_teammate","status":"running","isIdle":false,"identity":{"agentName":"Alpha","teamName":"t","color":"blue"}},"b":{"id":"b","type":"in_process_teammate","status":"running","isIdle":true,"identity":{"agentName":"Beta","teamName":"t","color":"red"}},"c":{"id":"c","type":"in_process_teammate","status":"pending","identity":{"agentName":"中文😀","teamName":"t"}}}`,
	} {
		objects = append(objects, presentationObject([]byte(content)))
	}
	rows := []map[string]any{}
	for _, theme := range []string{"dark", "light", "dark-daltonized", "light-daltonized", "dark-ansi", "light-ansi"} {
		for _, selected := range []bool{false, true} {
			for i, tasks := range objects {
				for _, width := range []int{100, 24} {
					s := NewState(nil, width, 35)
					s.Preferences.Theme = theme
					s.SetTeammateTasks(tasks)
					if selected {
						s.selectTerminalFooter("tasks")
					}
					row := map[string]any{"tasks": tasks, "theme": theme, "selected": selected, "width": width, "nativeRows": s.terminalTasksPillLines()}
					if i == len(objects)-1 && selected {
						s.footerTeammateIndex = 2
						row["index"] = 2
						row["nativeRows"] = s.terminalTasksPillLines()
					}
					if (i == len(objects)-1 || i == 8) && width == 24 && theme == "dark" {
						row["captureLayout"] = true
					}
					rows = append(rows, row)
				}
			}
			for _, count := range []int{0, 1, 3} {
				s := NewState(nil, 100, 35)
				s.Preferences.Theme = theme
				s.CachedTeams = []protocol.JSONObject{cachedTeam("owned", count)}
				if selected {
					s.selectTerminalFooter("teams")
				}
				members := protocol.NewJSONObject()
				members.Set("lead", []byte(`{"name":"team-lead"}`))
				for i := 0; i < count; i++ {
					members.Set(strings.Repeat("m", i+1), []byte(`{"name":"worker"}`))
				}
				team := protocol.NewJSONObject()
				data, _ := members.MarshalJSON()
				team.Set("teammates", data)
				rows = append(rows, map[string]any{"team": team, "theme": theme, "selected": selected, "width": 100, "nativeRows": s.terminalTeamsPillLines()})
			}
		}
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-footer-navigation.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_FOOTER_SOURCE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=100", "LINES=35", "FORCE_COLOR=3"}
	input, _ := json.Marshal(map[string]any{"home": home, "pills": rows})
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var expected []struct {
		Original           string
		Cells, NativeCells []json.RawMessage
		Layout             json.RawMessage
	}
	if json.Unmarshal(output, &expected) != nil || len(expected) != len(rows) {
		t.Fatal(string(output))
	}
	for i, result := range expected {
		if len(result.Layout) > 0 && string(result.Layout) != "null" {
			t.Log("Actual Source measured pill layout", string(result.Layout))
		}
		if !reflect.DeepEqual(result.Cells, result.NativeCells) {
			t.Errorf("Original whole pill profile%d %+v\nGo%s\nSource%s", i, rows[i], mustJSON(result.NativeCells), mustJSON(result.Cells))
		}
	}
}

func mustJSON(v any) string { data, _ := json.Marshal(v); return string(data) }
