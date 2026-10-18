package tui

import (
	"bytes"
	"context"
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

func TestTerminalTeamsWholeOriginalMountedDialogPaintSixThemes(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned original Source Bun required")
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-teams.fixture.ts")
	for _, theme := range []string{"dark", "light", "dark-daltonized", "light-daltonized", "dark-ansi", "light-ansi"} {
		for _, width := range []int{100, 47, 24} {
			home, _ := filepath.EvalSymlinks(t.TempDir())
			members := []protocol.JSONObject{presentationObject([]byte(`{"name":"one中文😀","agentId":"one@paint","tmuxPaneId":"%1","backendType":"tmux","mode":"plan","model":"deepseek-flash","cwd":"/private/owned/work","prompt":"original long prompt ` + strings.Repeat("中😀 word ", 16) + `","color":"blue","status":"running","isActive":true}`)), presentationObject([]byte(`{"name":"two","agentId":"two@paint","tmuxPaneId":"%2","backendType":"tmux","mode":"acceptEdits","status":"idle","isActive":false,"isHidden":true}`))}
			fileMembers := append([]protocol.JSONObject{presentationObject([]byte(`{"name":"team-lead","agentId":"lead@paint","tmuxPaneId":"%0"}`))}, members...)
			file := map[string]any{"name": "paint", "createdAt": 42, "leadAgentId": "lead@paint", "members": fileMembers, "hiddenPaneIds": []string{"%2"}}
			tasks := []protocol.JSONObject{presentationObject([]byte(`{"subject":"owned task 中文😀","description":"real owned task","status":"in_progress","owner":"one@paint","blocks":[],"blockedBy":[]}`)), presentationObject([]byte(`{"subject":"completed actual task","description":"completed task","status":"completed","owner":"one中文😀","blocks":[],"blockedBy":[]}`))}
			stages := []struct {
				level    string
				expanded bool
				selected int
			}{{"list", false, 0}, {"detail", false, 0}, {"detail", true, 0}, {"list", false, 0}, {"list", false, 1}}
			rows := [][]string{}
			for _, stage := range stages {
				data, _ := json.Marshal(map[string]any{"level": stage.level, "teamName": "paint", "members": members, "tasks": tasks, "memberName": members[0].String("name"), "supportsHideShow": true})
				p := &localPanelState{props: LocalPanel{Kind: "teams", Presentation: data}, selected: stage.selected, teamsPromptExpanded: stage.expanded}
				rows = append(rows, p.terminalTeamsLines(width, 80, theme, DefaultKeybindings("linux", false, KeybindingFeatures{})))
			}
			input, _ := json.Marshal(map[string]any{"home": home, "team": "paint", "file": file, "tasks": tasks, "width": width, "theme": theme, "nativeRows": rows, "insideTmux": true, "captureLayout": theme == "dark" && width == 47, "steps": []map[string]any{{"key": "\r", "text": "owned task 中文😀"}, {"key": "p"}, {"key": "\x1b[D", "text": "2 teammates"}, {"key": "\x1b[B"}}})
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_TEAMS_SOURCE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COLUMNS=" + strconv.Itoa(width), "LINES=80", "FORCE_COLOR=3", "CLAUDE_CODE_TMUX_TRUECOLOR=1", "PATH=/usr/bin:/bin", "USER_TYPE=external"}
			cmd.Stdin = bytes.NewReader(input)
			output, err := cmd.CombinedOutput()
			cancel()
			if err != nil {
				t.Fatalf("Source %s/%d %v %s", theme, width, err, output)
			}
			var source struct {
				Frames []struct {
					Cells, NativeCells []json.RawMessage
					Frame              string
					Layout             json.RawMessage
				}
				Blocked []string
			}
			if json.Unmarshal(output, &source) != nil || len(source.Frames) != len(stages) || len(source.Blocked) > 0 {
				t.Fatal(string(output))
			}
			for i, frame := range source.Frames {
				if len(frame.Layout) > 0 {
					t.Log("Actual whole Teams measured layout", string(frame.Layout))
				}
				if !reflect.DeepEqual(frame.Cells, frame.NativeCells) {
					t.Errorf("Original Teams %s width%d stage%d\nSource %q\nGo %q\nSource cells %s\nGo cells %s", theme, width, i, frame.Frame, strings.Join(rows[i], "\n"), mustJSON(frame.Cells), mustJSON(frame.NativeCells))
				}
			}
		}
	}
}
