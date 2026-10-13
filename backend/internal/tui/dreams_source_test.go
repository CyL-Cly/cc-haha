package tui

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceDreamDetailContentElapsedTimeAndKeys(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	if err = os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	now := float64(1700000000000)
	tasks := []protocol.JSONObject{}
	for _, status := range []string{"running", "completed", "failed", "killed"} {
		var task protocol.JSONObject
		raw, _ := json.Marshal(map[string]any{"id": "dream-test", "type": "dream", "status": status, "description": "dreaming", "startTime": now - 125000, "sessionsReviewing": 5, "filesTouched": []string{"/fixture/MEMORY.md", "/fixture/note.md"}, "turns": []any{map[string]any{"text": "", "toolUseCount": 4}, map[string]any{"text": "first hidden", "toolUseCount": 0}, map[string]any{"text": "second hidden", "toolUseCount": 0}, map[string]any{"text": "turn three", "toolUseCount": 1}, map[string]any{"text": "turn four", "toolUseCount": 2}, map[string]any{"text": "turn five", "toolUseCount": 0}, map[string]any{"text": "turn six", "toolUseCount": 3}, map[string]any{"text": "turn seven", "toolUseCount": 0}, map[string]any{"text": "turn eight", "toolUseCount": 1}}})
		_ = task.UnmarshalJSON(raw)
		tasks = append(tasks, task)
	}
	for _, status := range []string{"running", "completed"} {
		var task protocol.JSONObject
		raw, _ := json.Marshal(map[string]any{"id": "dream-empty", "type": "dream", "status": status, "description": "dreaming", "startTime": now + 100, "sessionsReviewing": 1, "filesTouched": []string{}, "turns": []any{map[string]any{"text": "", "toolUseCount": 4}}})
		_ = task.UnmarshalJSON(raw)
		tasks = append(tasks, task)
	}
	durations := []float64{-1, 0, .2, 1, 999, 1000, 59999, 60000, 119499, 119500, 3599499, 3599500, 86399500, 86400000, 172805000}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "now": now, "tasks": tasks, "durations": durations})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-dream-detail.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + root, "CC_HAHA_DREAM_DETAIL_FIXTURE=1", "PATH=/usr/bin:/bin", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var source struct {
		Rows []struct {
			Title, Subtitle, Content string
			Keys                     []struct {
				Key       string
				Prevented bool
				Calls     []string
			}
		}
		Durations []string
		Blocked   []string
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != len(tasks) || len(source.Blocked) > 0 {
		t.Fatal(err, string(out))
	}
	for i, task := range tasks {
		state := NewState(nil, 120, 40)
		state.Tick(time.UnixMilli(int64(now)))
		state.SetTeammateTasks(protocol.NewJSONObject(protocol.JSONField{Name: task.String("id"), Value: mustDreamJSON(t, task)}))
		state.DreamDialog = &dreamDialogState{taskID: task.String("id")}
		lines := state.dreamLines()
		if lines[0] != source.Rows[i].Title || lines[1] != source.Rows[i].Subtitle {
			t.Fatalf("Source subtitle/content boundary row %d\nSource %#v\nGo %#v", i, source.Rows[i], lines)
		}
		content := strings.Join(lines[2:len(lines)-1], "")
		if content != source.Rows[i].Content {
			t.Fatalf("Source detail row %d: Source %q Go %q", i, source.Rows[i].Content, content)
		}
		for _, key := range source.Rows[i].Keys {
			state.DreamDialog = &dreamDialogState{taskID: task.String("id")}
			name := key.Key
			if name == "escape" {
				name = "esc"
			}
			actions := state.HandleKey(Key{Name: name}, time.UnixMilli(int64(now)))
			if len(key.Calls) > 0 {
				switch key.Calls[0] {
				case "done":
					if state.DreamDialog != nil {
						t.Fatal("Source close not routed", i, key.Key)
					}
				case "back":
					if state.DreamDialog != nil {
						t.Fatal("single-task Source back did not close", i, key.Key)
					}
				case "kill":
					if len(actions) != 1 || actions[0].kind != actionTeammate || actions[0].teammate.Kind != "stop-dream" || actions[0].teammate.TaskID != task.String("id") {
						t.Fatal("Source kill not routed", i, key.Key)
					}
				}
			} else if len(actions) > 0 || state.DreamDialog == nil {
				t.Fatal("nonaction key became task command/close", i, key.Key, actions)
			}
		}
	}
	for i, ms := range durations {
		if actual := dreamDuration(ms); actual != source.Durations[i] {
			t.Fatalf("Source formatDuration %v: Source %q Go %q", ms, source.Durations[i], actual)
		}
	}
}

func mustDreamJSON(t *testing.T, task protocol.JSONObject) json.RawMessage {
	t.Helper()
	raw, err := task.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
