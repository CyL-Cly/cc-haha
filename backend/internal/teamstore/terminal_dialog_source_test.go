package teamstore

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
)

func TestTerminalTeamsActualSourceStatusesFirstPaneRemovalAndAtomicModes(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned Source Bun required")
	}
	rows := []map[string]any{
		{"name": "missing", "steps": []map[string]any{{"op": "statuses"}, {"op": "remove", "pane": "%1"}, {"op": "mode", "name": "worker", "mode": "plan"}, {"op": "modes", "updates": []any{}}}},
		{"name": "bad", "initial": "invalid-json", "steps": []map[string]any{{"op": "statuses"}}},
		{"name": "legacy", "initial": `{"name":"legacy","members":[{"name":"team-lead","tmuxPaneId":"%0"},{"name":"first","agentId":"one@legacy","tmuxPaneId":"%1","model":"","isActive":false,"mode":"weird","backendType":"unknown","future":{"keep":1}},{"name":"second","agentId":"two@legacy","tmuxPaneId":"%1","model":"fixture","isActive":0,"backendType":"tmux","prompt":"raw\ud800","mode":"plan"},{"name":"third","agentId":"three@legacy","tmuxPaneId":"%3","backendType":"iterm2","idleSince":"must-not-be-projected"}],"hiddenPaneIds":["%1","%1"],"future":{"keep":true}}`, "steps": []map[string]any{{"op": "statuses"}, {"op": "remove", "pane": "%1"}, {"op": "statuses"}, {"op": "remove", "pane": "absent"}, {"op": "mode", "name": "second", "mode": "plan"}, {"op": "mode", "name": "absent", "mode": "default"}, {"op": "modes", "updates": []map[string]any{{"memberName": "second", "mode": "default"}, {"memberName": "third", "mode": "acceptEdits"}, {"memberName": "second", "mode": "bypassPermissions"}}}, {"op": "statuses"}}},
		{"name": "duplicates", "initial": `{"members":[{"name":"worker","agentId":"first","mode":"default"},{"name":"worker","agentId":"second","mode":"plan"}],"future":7}`, "steps": []map[string]any{{"op": "mode", "name": "worker", "mode": "default"}, {"op": "mode", "name": "worker", "mode": "acceptEdits"}, {"op": "modes", "updates": []map[string]any{{"memberName": "worker", "mode": "acceptEdits"}}}}},
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-teams-files.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_TEAMS_FILES_SOURCE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "rows": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Rows    [][]json.RawMessage
		Blocked []string
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != len(rows) || len(source.Blocked) > 0 {
		t.Fatal(err, string(out))
	}
	native := t.TempDir()
	tasks, err := taskstore.New(taskstore.Options{ConfigDir: filepath.Join(native, ".claude"), PID: os.Getpid(), Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	config, err := NewConfig(ConfigOptions{ConfigDir: filepath.Join(native, ".claude"), TaskStore: tasks, PID: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range rows {
		t.Run(row["name"].(string), func(t *testing.T) {
			name := row["name"].(string)
			if initial, ok := row["initial"].(string); ok {
				_ = os.MkdirAll(config.Directory(name), 0700)
				_ = os.WriteFile(config.Path(name), []byte(initial), 0666)
			}
			for j, step := range row["steps"].([]map[string]any) {
				var result any
				var err error
				before, _ := os.Stat(config.Path(name))
				switch step["op"] {
				case "statuses":
					result, err = config.TeammateStatuses(name)
				case "remove":
					result, err = config.RemoveMemberByPaneID(name, raw(step["pane"]))
				case "mode":
					result, err = config.SetMemberMode(name, step["name"].(string), step["mode"].(string))
				case "modes":
					updates := []MemberModeUpdate{}
					if values, ok := step["updates"].([]map[string]any); ok {
						for _, update := range values {
							updates = append(updates, MemberModeUpdate{MemberName: update["memberName"].(string), Mode: update["mode"].(string)})
						}
					}
					result, err = config.SetMultipleMemberModes(name, updates)
				}
				var physical, mode, unchanged, errorText any
				if err != nil {
					errorText = err.Error()
				}
				if data, e := os.ReadFile(config.Path(name)); e == nil {
					physical = string(data)
					info, _ := os.Stat(config.Path(name))
					mode = uint32(info.Mode().Perm())
					if before != nil {
						unchanged = before.ModTime().Equal(info.ModTime())
					} else {
						unchanged = false
					}
				}
				actual := raw(map[string]any{"result": result, "error": errorText, "physical": physical, "mode": mode, "unchanged": unchanged})
				var a, b any
				_ = json.Unmarshal(actual, &a)
				_ = json.Unmarshal(source.Rows[i][j], &b)
				if !reflect.DeepEqual(a, b) {
					t.Fatalf("step%d/%s\nGo%s\nSource%s", j, step["op"], actual, source.Rows[i][j])
				}
			}
		})
	}
}
