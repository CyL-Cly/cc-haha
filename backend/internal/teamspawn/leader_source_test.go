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

func TestActualHeadlessLeaderPartitionFormattingAndShutdownStrictSchema(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := [][]protocol.JSONObject{{}, {inboxObject([]byte(`{"from":"worker","text":"actual result","color":"blue","summary":"not included"}`))}, {inboxObject([]byte(`{"from":"worker","text":"{\"type\":\"permission_request\",\"extra\":1}"}`)), inboxObject([]byte(`{"from":"other","text":"{\"type\":\"sandbox_permission_request\"}"}`)), inboxObject([]byte(`{"from":"peer","text":"{\"type\":\"idle_notification\"}"}`))}, {inboxObject([]byte(`{"from":"quoted\"<","text":"\ud800😀","color":"green\"","summary":"summary omitted"}`)), inboxObject([]byte(`{"from":12,"text":null,"color":false}`)), inboxObject([]byte(`{"text":"[\"permission_request\"]"}`))}}
	approvals := []string{`{"type":"shutdown_approved","requestId":"r","from":"worker","timestamp":"","paneId":"p","backendType":"in-process"}`, `{"type":"shutdown_approved","requestId":"r","from":"worker","timestamp":"t"}`, `{"type":"shutdown_approved","requestId":"","from":"worker","timestamp":"t"}`, `{"type":"shutdown_approved","requestId":"r","from":"","timestamp":"t"}`, `{"type":"shutdown_approved","requestId":"r","from":"worker","timestamp":null}`, `{"type":"shutdown_approved","requestId":"r","from":"worker","timestamp":"t","paneId":null}`, `{"type":"shutdown_approved","requestId":"r","from":"worker","timestamp":"t","backendType":"other"}`, `{"type":"shutdown_approved","requestId":"r","from":"worker","timestamp":"t","extra":true}`, `[]`, `null`, `not json`, `{"type":"shutdown_approved","requestId":"r","from":"worker","timestamp":"t","paneId":"","backendType":"process"}`}
	actual := []protocol.JSONObject{}
	for _, messages := range rows {
		part := PartitionLeadMailboxMessages(messages)
		text, err := FormatHeadlessTeammateMessages(part.Remaining)
		if err != nil {
			actual = append(actual, protocol.NewJSONObject(protocol.JSONField{Name: "partition", Value: rawJSON(part)}, protocol.JSONField{Name: "formatError", Value: rawJSON(true)}))
			continue
		}
		actual = append(actual, protocol.NewJSONObject(protocol.JSONField{Name: "partition", Value: rawJSON(part)}, protocol.JSONField{Name: "formatted", Value: text}))
	}
	accepted := []*protocol.JSONObject{}
	for _, message := range approvals {
		accepted = append(accepted, ShutdownApproved(message))
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-lead-partition.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_LEAD_PARTITION_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawJSON(map[string]any{"homeDir": home, "rows": rows, "approvals": approvals}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(output))
	}
	var source struct {
		Rows           json.RawMessage
		Approvals      json.RawMessage
		ShutdownPrompt string
		Capabilities   struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output), stderr.String())
	}
	compare := func(label string, a, b json.RawMessage) {
		t.Helper()
		var x, y any
		_ = json.Unmarshal(a, &x)
		_ = json.Unmarshal(b, &y)
		if !reflect.DeepEqual(x, y) {
			t.Fatalf("%s Go%s\nSource%s", label, a, b)
		}
	}
	compare("partition/format", rawJSON(actual), source.Rows)
	compare("shutdown schema", rawJSON(accepted), source.Approvals)
	if ShutdownTeamPrompt != source.ShutdownPrompt {
		t.Fatal("actual shutdown prompt changed")
	}
}
