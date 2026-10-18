package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"github.com/charmbracelet/x/ansi"
)

type terminalTeammateMessageSourceRow struct {
	Width        int                        `json:"width"`
	Height       int                        `json:"height"`
	Theme        string                     `json:"theme"`
	Transcript   bool                       `json:"transcript"`
	Preview      bool                       `json:"preview"`
	Expanded     string                     `json:"expanded"`
	Attachment   bool                       `json:"attachment"`
	Messages     []protocol.JSONObject      `json:"messages"`
	TeamsEnabled *bool                      `json:"teamsEnabled,omitempty"`
	GateSteps    []terminalTeammateGateStep `json:"gateSteps,omitempty"`
}

type terminalTeammateGateStep struct {
	TeamsEnabled bool `json:"teamsEnabled"`
	Transcript   bool `json:"transcript"`
}

type terminalTeammateGateFrame struct {
	TeamsEnabled bool     `json:"teamsEnabled"`
	Transcript   bool     `json:"transcript"`
	Rows         []string `json:"rows"`
	PaintRows    []string `json:"paintRows"`
}

type terminalTeammateMessageSourceFrame struct {
	TeamsEnabled  bool                        `json:"teamsEnabled"`
	Formatted     string                      `json:"formatted"`
	Parsed        []protocol.JSONObject       `json:"parsed"`
	LoadedRecords []protocol.JSONObject       `json:"loadedRecords"`
	Rows          []string                    `json:"rows"`
	PaintRows     []string                    `json:"paintRows"`
	GateFrames    []terminalTeammateGateFrame `json:"gateFrames"`
}

func terminalTeammateOriginalMessages(t *testing.T, rows []terminalTeammateMessageSourceRow, history ...bool) []terminalTeammateMessageSourceFrame {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual Source required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	name, flag := "native-terminal-teammate-message.fixture.ts", "CC_HAHA_TEAMMATE_MESSAGE_SOURCE=1"
	if len(history) > 0 && history[0] {
		name, flag = "native-terminal-teammate-history.fixture.ts", "CC_HAHA_TEAMMATE_HISTORY_SOURCE=1"
	}
	fixture, err := filepath.Abs(filepath.Join("../../../scripts/quality-gate/go-parity", name))
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]any{"home": home, "rows": rows})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=" + filepath.Join(home, "bin") + ":/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), flag, "CC_HAHA_AGENT_TEAMS_ENABLED=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=100", "LINES=36", "FORCE_COLOR=3"}
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("Original mounted Message %v stdout %.2000s stderr %.3000s", err, stdout.Bytes(), stderr.Bytes())
	}
	if stderr.Len() != 0 {
		t.Fatalf("Original mounted Message stderr %q", stderr.String())
	}
	var result struct {
		Results     []terminalTeammateMessageSourceFrame `json:"results"`
		Blocked     []string                             `json:"blocked"`
		SecureReads []string                             `json:"secureReads"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || len(result.Results) != len(rows) || len(result.Blocked)+len(result.SecureReads) != 0 {
		t.Fatalf("Original mounted Message result %v %.4000s", err, stdout.Bytes())
	}
	return result.Results
}

func terminalTeammateRowsFromParsed(parsed []protocol.JSONObject) []protocol.JSONObject {
	rows := []protocol.JSONObject{}
	for _, message := range parsed {
		row := protocol.NewJSONObject(protocol.JSONField{Name: "from", Value: message.Get("teammateId")}, protocol.JSONField{Name: "text", Value: message.Get("content")})
		for _, key := range []string{"summary", "color"} {
			if value := message.Get(key); value != nil {
				row.Set(key, value)
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func TestActualSourceDeliveredTeammateWholeMessagePreviewSummaryTranscriptAndStyles(t *testing.T) {
	makeMessage := func(from, text, summary, color string) protocol.JSONObject {
		result := protocol.JSONObject{}
		for _, item := range []struct{ key, value string }{{"from", from}, {"text", text}, {"summary", summary}, {"color", color}} {
			if item.value != "" || item.key == "text" {
				value, _ := json.Marshal(item.value)
				result.Set(item.key, value)
			}
		}
		return result
	}
	rows := []terminalTeammateMessageSourceRow{}
	for themeIndex, theme := range []string{"dark", "light", "dark-daltonized", "light-daltonized", "dark-ansi", "light-ansi"} {
		for _, width := range []int{100, 47, 24} {
			for _, transcript := range []bool{false, true} {
				for _, attachment := range []bool{false, true} {
					messages := []protocol.JSONObject{
						makeMessage("worker", "full delivered text\nactual 汉字😀 wrapped body with words", "delivered summary", ""),
						makeMessage("A\"&<", "actual escaped body", "escaped \"&< summary", []string{"red", "blue", "green", "purple", "orange", "pink"}[themeIndex]),
						makeMessage("no-summary", "body hidden outside transcript", "", "cyan"),
						makeMessage("hidden-idle", `{"type":"idle_notification"}`, "never rendered", ""),
						makeMessage("hidden-terminated", `{"type":"teammate_terminated","message":"never rendered"}`, "", ""),
					}
					rows = append(rows, terminalTeammateMessageSourceRow{width, 80, theme, transcript, attachment, "none", attachment, messages, nil, nil})
				}
			}
		}
	}
	for _, width := range []int{100, 47, 24} {
		for _, transcript := range []bool{false, true} {
			for _, preview := range []bool{false, true} {
				rows = append(rows, terminalTeammateMessageSourceRow{width, 80, "dark", transcript, preview, "teammates", false, []protocol.JSONObject{makeMessage("worker-with-long-name", "actual body", "Actual delivered summary with 汉字😀 many words and next bit", "red")}, nil, nil})
			}
		}
	}
	frames := terminalTeammateOriginalMessages(t, rows)
	for i, row := range rows {
		t.Run(fmt.Sprintf("%d-%s-%d-transcript%t-preview%t-attachment%t", i, row.Theme, row.Width, row.Transcript, row.Preview, row.Attachment), func(t *testing.T) {
			frame := frames[i]
			parsed := teamspawn.ParseInteractiveTeammateXML(frame.Formatted)
			if !terminalTeammateParsedEqual(parsed, frame.Parsed) {
				t.Fatalf("Real formatter/parser escaping differs Source%v Go%v", frame.Parsed, parsed)
			}
			messages := row.Messages
			if !row.Attachment {
				messages = terminalTeammateRowsFromParsed(parsed)
			}
			before, _ := json.Marshal(messages)
			state := NewState(nil, row.Width, row.Height)
			state.Preferences = TerminalPreferences{Theme: row.Theme, ReducedMotion: true}
			state.ShowTeammatePreview, state.Transcript = row.Preview, row.Transcript
			state.SetTeammateTasks(teammateTestTasks())
			state.Teammates.ExpandedView = row.Expanded
			state.Apply(Event{Kind: TeammateMessages, Result: before})
			got := state.transcriptLines()
			if !reflect.DeepEqual(terminalTaskListCells(frame.PaintRows, state.Width), terminalTaskListCells(got, state.Width)) {
				t.Fatalf("Original complete Message cells/styles Source%q Go%q", frame.Rows, got)
			}
			view := strings.Split(state.View("", false).Content, "\n")
			if len(view) < len(got) || !reflect.DeepEqual(terminalTaskListCells(frame.PaintRows, state.Width), terminalTaskListCells(view[:len(got)], state.Width)) {
				t.Fatalf("Actual State.View lost original delivered Message rows Source%q View%q", frame.Rows, view)
			}
			after, _ := json.Marshal(messages)
			if !bytes.Equal(before, after) {
				t.Fatal("Rendering mutated delivered message input")
			}
		})
	}
}

func terminalTeammateParsedEqual(actual, expected []protocol.JSONObject) bool {
	if len(actual) != len(expected) {
		return false
	}
	for index, object := range actual {
		if len(object.OwnProperties()) != len(expected[index].OwnProperties()) {
			return false
		}
		for _, field := range expected[index].OwnProperties() {
			value, lookupErr := object.GetPropertyJSON(field.NameJSON)
			left, leftErr := protocol.DecodeJavaScriptJSONString(value)
			right, rightErr := protocol.DecodeJavaScriptJSONString(field.Value)
			if lookupErr != nil || leftErr != nil || rightErr != nil || !reflect.DeepEqual(left, right) {
				return false
			}
		}
	}
	return true
}

func TestActualSourceDeliveredTeammateLongViewportAndReturnFromViewedTranscript(t *testing.T) {
	long := []string{}
	for i := 0; i < 30; i++ {
		long = append(long, fmt.Sprintf("actual body line %02d", i))
	}
	messages := []protocol.JSONObject{presentationObject(mustLocalMessageJSON(map[string]any{"from": "worker", "text": strings.Join(long, "\n"), "summary": "delivered summary"}))}
	rows := []terminalTeammateMessageSourceRow{}
	for _, width := range []int{100, 24} {
		for _, transcript := range []bool{false, true} {
			rows = append(rows, terminalTeammateMessageSourceRow{width, 12, "dark", transcript, false, "none", false, messages, nil, nil})
		}
	}
	frames := terminalTeammateOriginalMessages(t, rows)
	for i, row := range rows {
		state := NewState(nil, row.Width, row.Height)
		state.Preferences = TerminalPreferences{Theme: row.Theme, ReducedMotion: true}
		state.SetTeammateTasks(teammateTestTasks())
		state.Teammates.ExpandedView = "none"
		state.Teammates.ViewSelectionMode, state.Teammates.ViewingAgentTaskID = "viewing-agent", "actual-task"
		state.Apply(Event{Kind: TeammateMessages, Result: mustLocalMessageJSON(messages)})
		viewed := ansi.Strip(state.View("", false).Content)
		compactViewed := strings.Join(strings.Fields(viewed), "")
		if !strings.Contains(compactViewed, "ACTUAL_TEAMMATE_TRANSCRIPT") || strings.Contains(compactViewed, "deliveredsummary") || strings.Contains(compactViewed, "actualbodyline") {
			t.Fatal("Delivered leader inbox leaked into actual teammate viewport", viewed)
		}
		state.Teammates.Exit()
		state.Transcript = row.Transcript
		entries := state.Entries
		state.Entries = nil
		footer := strings.Split(ansi.Strip(state.View("", false).Content), "\n")
		state.Entries = entries
		view := strings.Split(ansi.Strip(state.View("", false).Content), "\n")
		footerStart := len(view) - len(footer)
		if footerStart < 0 || !reflect.DeepEqual(footer, view[footerStart:]) {
			t.Fatal("Actual spinner/input footer changed while projecting delivered messages", footer, view)
		}
		if expected := min(len(frames[i].Rows), max(0, state.Height-len(footer))); footerStart != expected {
			t.Fatalf("Actual viewport omitted Source Message rows: want%d body rows, got%d", expected, footerStart)
		}
		want := frames[i].Rows
		if len(want) > footerStart {
			want = want[len(want)-footerStart:]
		}
		body := append([]string(nil), view[:footerStart]...)
		for index := range body {
			body[index] = strings.TrimRight(body[index], " ")
		}
		if !reflect.DeepEqual(want, body) {
			t.Fatalf("Actual State.View viewport must retain original Message suffix width%d transcript%t Source%q Go%q", row.Width, row.Transcript, want, body)
		}
	}
}
