package teamnavigation

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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestOriginalTeammateNavigationSortingWrappingRefreshKillAndViewAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Source Bun required")
	}
	task := func(id, name, status string) protocol.JSONObject {
		return object([]byte(`{"id":"` + id + `","type":"in_process_teammate","status":"` + status + `","identity":{"agentName":"` + name + `"},"extra":{"preserved":true}}`))
	}
	tasks := protocol.NewJSONObject()
	for _, value := range []protocol.JSONObject{task("second", "Zoo", "running"), task("first", "alpha", "running"), task("done", "old", "completed")} {
		data, _ := value.MarshalJSON()
		tasks.Set(value.String("id"), data)
	}
	type step struct {
		Key   *Key                 `json:"key,omitempty"`
		Tasks *protocol.JSONObject `json:"tasks,omitempty"`
	}
	type row struct {
		Initial State  `json:"initial"`
		Steps   []step `json:"steps"`
	}
	rows := []row{}
	for _, keys := range [][]Key{
		{{"down", true}, {"down", true}, {"return", false}, {"escape", false}},
		{{"up", true}, {"up", true}, {"return", false}, {"up", true}, {"f", false}},
		{{"down", true}, {"down", true}, {"k", false}, {"escape", false}},
		{{"down", true}, {"up", true}, {"return", false}},
		{{"down", true}, {"down", true}, {"down", true}, {"down", true}, {"down", true}, {"return", false}},
		{{"f", false}, {"k", false}, {"escape", false}, {"x", false}},
	} {
		initial := New()
		initial.Tasks = tasks
		item := row{Initial: initial}
		for _, key := range keys {
			value := key
			item.Steps = append(item.Steps, step{Key: &value})
		}
		rows = append(rows, item)
	}
	for _, status := range []string{"running", "completed", "pending", "killed", "failed", "unknown"} {
		initial := New()
		initial.Tasks = tasks
		initial.ViewingAgentTaskID = "first"
		initial.ViewSelectionMode = "viewing-agent"
		initial.SelectedIPAgentIndex = 0
		replacement := protocol.NewJSONObject()
		v := task("first", "alpha", status)
		data, _ := v.MarshalJSON()
		replacement.Set("first", data)
		rows = append(rows, row{initial, []step{{Tasks: &replacement}}})
	}
	initial := New()
	initial.Tasks = tasks
	initial.ViewingAgentTaskID = "first"
	initial.ViewSelectionMode = "viewing-agent"
	empty := protocol.NewJSONObject()
	rows = append(rows, row{initial, []step{{Tasks: &empty}}})
	background := protocol.NewJSONObject()
	background.Set("bash", []byte(`{"id":"bash","type":"local_bash","status":"running"}`))
	initial = New()
	initial.Tasks = background
	k := Key{"down", true}
	rows = append(rows, row{initial, []step{{Key: &k}}})
	observations := [][]protocol.JSONObject{}
	for _, row := range rows {
		state := row.Initial
		steps := []protocol.JSONObject{}
		for _, step := range row.Steps {
			actions := []Action{}
			prevented := false
			oldView := state.ViewingAgentTaskID
			if step.Tasks != nil {
				state.Refresh(*step.Tasks)
				if oldView != "" && state.ViewingAgentTaskID == "" {
					actions = append(actions, Action{"exit", ""})
				}
			}
			if step.Key != nil {
				var action Action
				prevented, action = state.Handle(*step.Key)
				if action.Kind != "" {
					actions = append(actions, action)
				}
			}
			data, _ := json.Marshal(map[string]any{"state": state, "prevented": prevented, "actions": actions})
			steps = append(steps, object(data))
		}
		observations = append(observations, steps)
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-navigation.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_NAVIGATION_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct{ Observations [][]protocol.JSONObject }
	if err = json.Unmarshal(output, &source); err != nil || len(source.Observations) != len(rows) {
		t.Fatal(err, string(output))
	}
	for i, got := range observations {
		var a, b any
		ga, _ := json.Marshal(got)
		sa, _ := json.Marshal(source.Observations[i])
		_ = json.Unmarshal(ga, &a)
		_ = json.Unmarshal(sa, &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("row %d Source navigation differs Go %s Source %s", i, ga, sa)
		}
	}
}
