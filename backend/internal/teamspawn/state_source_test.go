package teamspawn

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestInProcessTeammateUIStateCapToolIDsAndTerminalCallbacksAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	initial := inboxObject([]byte(`{"id":"tfixture","status":"running","pendingUserMessages":["pending"],"messages":[],"isIdle":false,"extra":{"original":true}}`))
	rows := []protocol.JSONObject{}
	for i := 0; i < 53; i++ {
		rows = append(rows, inboxObject(rawJSON(map[string]any{"type": "user", "uuid": i, "message": map[string]any{"content": "actual prompt"}})))
	}
	for _, text := range []string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"one"},{"type":"tool_use","id":"two"},{"type":"tool_use","id":"one"}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"one"},{"type":"text","text":"original"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"one"}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"two"},{"type":"tool_result","tool_use_id":"one"}]}}`,
	} {
		rows = append(rows, inboxObject([]byte(text)))
	}
	progress, actual := []protocol.JSONObject{}, []protocol.JSONObject{}
	task := initial
	for i, row := range rows {
		p := inboxObject(rawJSON(map[string]any{"toolUseCount": i, "tokenCount": 100 + i, "recentActivities": []any{}}))
		progress = append(progress, p)
		var err error
		task, err = ObserveMessage(task, row, p)
		if err != nil {
			t.Fatal(err)
		}
		actual = append(actual, task.Clone())
	}
	finishInputs := []protocol.JSONObject{}
	finishes := []protocol.JSONObject{}
	for _, status := range []string{"completed", "failed"} {
		for _, input := range []protocol.JSONObject{task, initial, inboxObject([]byte(`{"id":"tfixture","status":"killed","notified":true,"messages":[],"extra":1}`))} {
			finishInputs = append(finishInputs, protocol.NewJSONObject(protocol.JSONField{Name: "status", Value: quoted(status)}, protocol.JSONField{Name: "task", Value: rawJSON(input)}, protocol.JSONField{Name: "now", Value: rawJSON(1700000000123)}, protocol.JSONField{Name: "failure", Value: quoted("actual failure")}))
			value, changed, err := FinishTask(input, status, 1700000000123, "actual failure")
			if err != nil {
				t.Fatal(err)
			}
			finishes = append(finishes, inboxObject(rawJSON(map[string]any{"task": value, "changed": changed})))
		}
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-inprocess-teammate-state.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_INPROCESS_TEAM_STATE_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawJSON(map[string]any{"homeDir": home, "task": initial, "messages": rows, "progress": progress, "finishes": finishInputs}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(output))
	}
	var report struct {
		Observations []protocol.JSONObject
		Finishes     []protocol.JSONObject
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &report); err != nil || len(report.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output), stderr.String())
	}
	compare := func(label string, a, b any) {
		t.Helper()
		var x, y any
		_ = json.Unmarshal(rawJSON(a), &x)
		_ = json.Unmarshal(rawJSON(b), &y)
		if !reflect.DeepEqual(x, y) {
			t.Fatalf("%s Go %s\nSource %s", label, rawJSON(a), rawJSON(b))
		}
	}
	compare("observations", actual, report.Observations)
	compare("finishes", finishes, report.Finishes)
}
