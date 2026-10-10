package teamspawn

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLastPeerDMSummaryCurrentWakeBoundaryAndUTF16AgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	assistant := func(to, message string, summary any) protocol.JSONObject {
		fields := protocol.NewJSONObject(protocol.JSONField{Name: "to", Value: quoted(to)}, protocol.JSONField{Name: "message", Value: quoted(message)})
		if summary != nil {
			fields.Set("summary", rawJSON(summary))
		}
		return inboxObject(rawJSON(map[string]any{"type": "assistant", "message": map[string]any{"content": []map[string]any{{"type": "tool_use", "name": "SendMessage", "input": fields}}}}))
	}
	wake := inboxObject([]byte(`{"type":"user","message":{"content":"actual next wake"}}`))
	result := inboxObject([]byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"actual"}]}}`))
	rows := [][]protocol.JSONObject{{}, {assistant("peer", "content", nil)}, {assistant("TEAM-LEAD", "ignore", nil)}, {assistant("*", "ignore", nil)}, {assistant("peer", "ignored", "")}, {assistant("peer", strings.Repeat("a", 79)+"😀tail", nil)}, {assistant("old", "prior", nil), wake}, {wake, assistant("first", "one", nil), assistant("last", "two", "actual summary"), result}}
	actual := []json.RawMessage{}
	for _, row := range rows {
		value, err := LastPeerDMSummary(row)
		if err != nil {
			t.Fatal(err)
		}
		actual = append(actual, value)
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-inprocess-teammate-history.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_INPROCESS_TEAM_HISTORY_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawJSON(map[string]any{"homeDir": home, "rows": []any{}, "summaries": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(output))
	}
	var report struct {
		Summaries    json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &report); err != nil || len(report.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output), stderr.String())
	}
	want := rawJSON(actual)
	var a, b any
	_ = json.Unmarshal(want, &a)
	_ = json.Unmarshal(report.Summaries, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("Go %s\nSource %s", want, report.Summaries)
	}
}
