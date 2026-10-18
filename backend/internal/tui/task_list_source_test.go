package tui

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualMountedSourceTaskListREPLSpinnerModalSixThemeCellsAndStyles(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source Bun required")
	}
	tasks := []protocol.JSONObject{}
	for _, text := range []string{
		`{"subject":"task 1 completed","status":"completed"}`,
		`{"subject":"task 2 blocked long 中文😀 word word word word word word word word","status":"pending","blockedBy":["3","7"]}`,
		`{"subject":"task 3 running","status":"in_progress"}`,
		`{"subject":"task 4 ready","status":"pending"}`,
		`{"subject":"task 5 completed","status":"completed"}`,
		`{"subject":"task 6 ready","status":"pending"}`,
		`{"subject":"task 7 running","status":"in_progress"}`,
		`{"subject":"task 8 pending","status":"pending"}`,
	} {
		tasks = append(tasks, presentationObject([]byte(text)))
	}
	requests := []map[string]any{}
	for _, theme := range []string{"dark", "light", "dark-daltonized", "light-daltonized", "dark-ansi", "light-ansi"} {
		for _, width := range []int{100, 24} {
			for _, height := range []int{10, 17, 36} {
				requests = append(requests, map[string]any{"tasks": tasks, "theme": theme, "width": width, "height": height, "steps": []any{"\x14", map[string]any{"props": map[string]any{"loading": true}}, map[string]any{"props": map[string]any{"loading": false, "localJSX": true}}, map[string]any{"props": map[string]any{"localJSX": false}}}})
			}
		}
	}
	background := presentationObject([]byte(`{"alpha":{"id":"alpha","type":"in_process_teammate","status":"running","isIdle":false,"identity":{"agentName":"Alpha","agentId":"Alpha@owned","teamName":"owned","color":"blue"},"progress":{"recentActivities":[{"isRead":true},{"isSearch":true},{"isSearch":true},{"isRead":true}],"lastActivity":{"activityDescription":"fallback"}}},"beta":{"id":"beta","type":"in_process_teammate","status":"stopped","identity":{"agentName":"Beta","agentId":"Beta@owned","teamName":"owned","color":"red"}}}`))
	context := presentationObject([]byte(`{"teamName":"owned","leadAgentId":"team-lead@owned","teammates":{"alpha":{"name":"Alpha","color":"blue"},"beta":{"name":"Beta","color":"red"}}}`))
	owned := []protocol.JSONObject{presentationObject([]byte(`{"subject":"Actual running owner","owner":"Alpha","status":"in_progress"}`)), presentationObject([]byte(`{"subject":"Actual stopped owner","owner":"Beta","status":"in_progress"}`)), presentationObject([]byte(`{"subject":"Actual owner id","owner":"Alpha@owned","status":"in_progress"}`))}
	for _, width := range []int{100, 59, 24} {
		requests = append(requests, map[string]any{"tasks": owned, "teamContext": context, "background": background, "width": width, "height": 36, "theme": "dark", "loading": true, "steps": []any{"\x14", map[string]any{"background": protocol.NewJSONObject()}, map[string]any{"props": map[string]any{"loading": false}}}})
	}
	recent := []protocol.JSONObject{}
	for i := 0; i < 12; i++ {
		status := "pending"
		if i < 4 {
			status = "in_progress"
		}
		recent = append(recent, presentationObject([]byte(`{"subject":"Recent completion order `+strconv.Itoa(i+1)+`","status":"`+status+`"}`)))
	}
	requests = append(requests, map[string]any{"tasks": recent, "width": 100, "height": 17, "theme": "dark", "steps": []any{"\x14", map[string]any{"update": map[string]any{"id": "12", "status": "completed"}}, map[string]any{"advanceClock": 31000}, map[string]any{"update": map[string]any{"id": "12", "status": "pending"}}, map[string]any{"update": map[string]any{"id": "12", "status": "completed"}}}})
	requests = append(requests, map[string]any{"tasks": []protocol.JSONObject{}, "width": 100, "height": 36, "theme": "dark", "steps": []any{"\x14", "\x14"}})
	requests = append(requests, map[string]any{"tasks": []protocol.JSONObject{}, "width": 100, "height": 36, "theme": "dark", "steps": []any{"\x14", map[string]any{"create": map[string]any{"subject": "Completed file while hidden", "status": "completed"}}, map[string]any{"create": map[string]any{"subject": "Pending file restores visibility", "status": "pending"}}}})
	requests = append(requests, map[string]any{"tasks": []protocol.JSONObject{}, "width": 100, "height": 36, "theme": "dark", "steps": []any{map[string]any{"toolUpdate": map[string]any{"taskId": "99", "status": "in_progress"}}}})
	for _, menu := range []string{"model", "doctor", "ide", "model-shortcut"} {
		requests = append(requests, map[string]any{"tasks": owned, "width": 100, "height": 36, "theme": "dark", "steps": []any{"\x14", map[string]any{"props": map[string]any{"menu": menu}}, map[string]any{"props": map[string]any{"menu": ""}}}})
	}
	// The matrix shares the original process-global config between profiles.
	// Seed this new collapsed-view profile through the original durable writer,
	// rather than inheriting an earlier profile's explicit CtrlT preference.
	requests = append(requests, map[string]any{"tasks": []protocol.JSONObject{presentationObject([]byte(`{"subject":"ACTUAL_TEAMMATE_SHARED_TASK","description":"Original child write reaches mounted parent store","status":"pending","blocks":[],"blockedBy":[]}`))}, "global": map[string]any{"showExpandedTodos": false}, "expanded": "none", "collapsedHint": true, "width": 100, "height": 36, "theme": "dark", "steps": []any{map[string]any{"props": map[string]any{"loading": true}}, map[string]any{"props": map[string]any{"loading": false}}, "\x14"}})
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-task-list.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TASK_LIST_SOURCE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=100", "LINES=36", "FORCE_COLOR=3"}
	input, _ := json.Marshal(map[string]any{"home": home, "rows": requests})
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal("Actual mounted TaskList/REPL/Spinner", err, string(output))
	}
	var source struct {
		Results [][]struct {
			ExpandedView string
			Tasks        []protocol.JSONObject
			ShowSpinner  bool
			Props        protocol.JSONObject
			Background   protocol.JSONObject
			TeamContext  protocol.JSONObject
			Global       protocol.JSONObject
			TeamsEnabled bool
			Now          int64
			PaintRows    []string
			Rows         []string
		}
		Caps struct{ Blocked, SecureReads []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Results) != len(requests) || len(source.Caps.Blocked) > 0 || len(source.Caps.SecureReads) > 0 {
		t.Fatal(err, string(output))
	}
	for i, request := range requests {
		s := NewState(nil, request["width"].(int), request["height"].(int))
		s.Preferences.Theme = request["theme"].(string)
		for j, frame := range source.Results[i] {
			s.dreamClock = time.UnixMilli(frame.Now)
			s.SetTeammateTasks(frame.Background)
			s.taskTeamContext, s.taskTeamsEnabled = frame.TeamContext, frame.TeamsEnabled
			s.TaskItems = frame.Tasks
			s.setTerminalExpandedView(frame.ExpandedView)
			s.ActiveTurn = ""
			if string(frame.Props.Get("loading")) == "true" {
				s.ActiveTurn = "actual-loading"
			}
			s.LocalPanel = nil
			s.ModelPicker, s.Doctor, s.IDEPicker = nil, nil, nil
			switch frame.Props.String("menu") {
			case "model", "model-shortcut":
				s.OpenModelPicker(ModelPicker{ID: "actual-model", Standalone: frame.Props.String("menu") == "model"})
			case "doctor":
				s.Doctor = &DoctorPanel{Text: "actual Doctor sibling"}
			case "ide":
				s.OpenIDEPicker(IDEPicker{ID: "actual-ide", Mode: "connect"})
			}
			if string(frame.Props.Get("localJSX")) == "true" {
				s.openLocalPanel(&LocalPanel{ID: "actual-local-jsx", Kind: "help", OriginKnown: true, LocalJSXCommand: true})
			}
			s.syncTerminalTaskListMount()
			want := frame.PaintRows
			if request["collapsedHint"] == true && frame.ExpandedView == "none" {
				if len(s.expandedTaskLines()) != 0 || string(frame.Global.Get("showExpandedTodos")) != "false" {
					t.Fatal("A task-store frame expanded the original collapsed view", i, j, frame.Global, s.expandedTaskLines())
				}
				if frame.ShowSpinner {
					if len(frame.Rows) != 3 || frame.Rows[2] != "  ⎿ \u00a0Next: ACTUAL_TEAMMATE_SHARED_TASK" {
						t.Fatal("Original collapsed Spinner hint changed", frame.Rows)
					}
					var hint []string
					for _, line := range strings.Split(s.View("", false).Content, "\n") {
						if strings.Contains(line, "Next: ACTUAL_TEAMMATE_SHARED_TASK") {
							hint = append(hint, line)
						}
					}
					if !reflect.DeepEqual(terminalTaskListCells(frame.PaintRows[2:], s.Width), terminalTaskListCells(hint, s.Width)) {
						t.Fatal("Collapsed Spinner Next hint differs from original Source", frame.Rows, hint)
					}
				} else if len(frame.Rows) != 0 || strings.Contains(s.View("", false).Content, "ACTUAL_TEAMMATE_SHARED_TASK") {
					t.Fatal("Idle collapsed task view retained a Spinner hint", frame.Rows, s.View("", false).Content)
				}
				continue
			}
			if frame.ShowSpinner {
				if len(want) >= 2 {
					want = want[2:]
				} else {
					want = nil
				}
			}
			got := s.expandedTaskLines()
			if len(want) == 0 && len(got) == 0 {
				continue
			}
			if !reflect.DeepEqual(terminalTaskListCells(want, s.Width), terminalTaskListCells(got, s.Width)) {
				t.Errorf("profile%d frame%d %v\nSource%q\nGo%q", i, j, request, frame.Rows, got)
			}
		}
	}
}

func terminalTaskListCells(rows []string, width int) [][]terminalStatusLineCell {
	if len(rows) == 0 {
		return nil
	}
	return terminalFullscreenHelpCells(strings.Join(rows, "\n"), width)
}
