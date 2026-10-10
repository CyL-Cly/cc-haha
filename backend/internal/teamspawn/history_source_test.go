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

func TestActualRunAgentForkFilterPreservesRecordsAndDropsWholeIncompleteAssistant(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	bodies := []string{
		`[]`,
		`[{"type":"assistant","message":{"content":[{"type":"text","text":"drops text too"},{"type":"tool_use","id":"missing","name":"Write"}]}}]`,
		`[{"type":"assistant","message":{"content":[{"type":"tool_use","id":"complete"}]}},{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"complete"}]}}]`,
		`[{"type":"assistant","message":{"content":[{"type":"tool_use","id":"one"},{"type":"tool_use","id":"two"}]}},{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"one"}]}}]`,
		`[{"type":"assistant","message":{"content":[{"type":"tool_use","id":""}]}},{"type":"user","message":{"content":"plain"}},{"type":"attachment","attachment":{"type":"actual","keep":true}}]`,
		`[{"type":"assistant","message":{"content":[{"type":"tool_use","id":"\ud83d\ude00"}]}},{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"😀"}]}}]`,
		`[{"type":"assistant","message":{"content":[{"type":"text","text":"complete"}]}},{"type":"assistant","message":{"content":"legacy string"}}]`,
		`[{"type":"assistant","message":{"content":[null]}}]`,
	}
	rows := [][]protocol.JSONObject{}
	actual := []map[string]any{}
	for _, body := range bodies {
		var row []protocol.JSONObject
		if err := json.Unmarshal([]byte(body), &row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
		filtered, err := FilterIncompleteContext(row)
		var errorText any
		if err != nil {
			errorText = err.Error()
		}
		actual = append(actual, map[string]any{"result": filtered, "error": errorText})
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-inprocess-teammate-history.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_INPROCESS_TEAM_HISTORY_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawJSON(map[string]any{"homeDir": home, "rows": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(output))
	}
	var report struct {
		Rows         json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &report); err != nil || len(report.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output), stderr.String())
	}
	want := rawJSON(actual)
	var a, b any
	_ = json.Unmarshal(want, &a)
	_ = json.Unmarshal(report.Rows, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("Go %s\nSource %s", want, report.Rows)
	}
}
