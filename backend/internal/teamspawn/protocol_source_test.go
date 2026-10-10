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
	"time"
)

func TestStructuredShutdownAndLeaderPlanResponsesAgainstActualSourceToolAndPhysicalMailbox(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []map[string]any{}
	for i, row := range []map[string]any{
		{"input": map[string]any{"to": "worker", "message": map[string]any{"type": "shutdown_request", "reason": "actual shutdown"}}},
		{"worker": true, "input": map[string]any{"to": "team-lead", "message": map[string]any{"type": "shutdown_response", "request_id": "actual-request", "approve": true}}},
		{"worker": true, "input": map[string]any{"to": "team-lead", "message": map[string]any{"type": "shutdown_response", "request_id": "actual-request", "approve": false, "reason": "still working"}}},
		{"mode": "plan", "input": map[string]any{"to": "worker", "message": map[string]any{"type": "plan_approval_response", "request_id": "actual-plan", "approve": true}}},
		{"mode": "acceptEdits", "input": map[string]any{"to": "worker", "message": map[string]any{"type": "plan_approval_response", "request_id": "actual-plan", "approve": true}}},
		{"input": map[string]any{"to": "worker", "message": map[string]any{"type": "plan_approval_response", "request_id": "actual-plan", "approve": false, "feedback": "revise design"}}},
		{"worker": true, "input": map[string]any{"to": "peer", "message": map[string]any{"type": "plan_approval_response", "request_id": "actual-plan", "approve": true}}},
		{"worker": true, "input": map[string]any{"to": "peer", "message": map[string]any{"type": "plan_approval_response", "request_id": "actual-plan", "approve": false}}},
	} {
		row["now"] = 1700000000000 + i
		rows = append(rows, row)
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-inprocess-teammate-protocol.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_INPROCESS_TEAM_PROTOCOL_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawJSON(map[string]any{"homeDir": home, "rows": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(output))
	}
	var report struct {
		Rows         []protocol.JSONObject
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &report); err != nil || len(report.Capabilities.Blocked) > 0 || len(report.Rows) != len(rows) {
		t.Fatal(err, string(output), stderr.String())
	}
	for i, row := range rows {
		input := inboxObject(rawJSON(row["input"]))
		message, _ := input.Object("message")
		worker, _ := row["worker"].(bool)
		mode, _ := row["mode"].(string)
		if mode == "" {
			mode = "default"
		}
		now := int64(1700000000000 + i)
		identity := ProtocolIdentity{Leader: !worker, LeaderMode: mode, Now: float64(now), Timestamp: time.UnixMilli(now).UTC().Format("2006-01-02T15:04:05.000Z")}
		if worker {
			identity.AgentName = "worker"
			identity.AgentID = "worker@actual"
			identity.Color = "blue"
			identity.PaneID = quoted("in-process")
			identity.BackendType = quoted("in-process")
			identity.InProcessTaskFound = true
		}
		native, err := StructuredMessage(input.String("to"), message, identity)
		original := report.Rows[i]
		var failure any
		if err != nil {
			failure = err.Error()
		}
		var sourceFailure any
		_ = json.Unmarshal(original.Get("failure"), &sourceFailure)
		if !reflect.DeepEqual(failure, sourceFailure) {
			t.Fatal("error priority", failure, sourceFailure)
		}
		if err != nil {
			continue
		}
		var a, b any
		_ = json.Unmarshal(rawJSON(native.Output), &a)
		_ = json.Unmarshal(original.Get("result"), &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("case%d Go%s\nSource%s", i, rawJSON(native.Output), original.Get("result"))
		}
		var envelopes []protocol.JSONObject
		key := "worker"
		if native.Recipient == "team-lead" {
			key = "leader"
		}
		_ = json.Unmarshal(original.Get(key), &envelopes)
		if len(envelopes) != 1 {
			t.Fatal("actual Source physical mailbox", original)
		}
		envelopes[0].Delete("read")
		if envelopes[0].String("id") == "" {
			t.Fatal("actual mailbox writer ID missing")
		}
		envelopes[0].Delete("id")
		_ = json.Unmarshal(rawJSON(native.Envelope), &a)
		_ = json.Unmarshal(rawJSON(envelopes[0]), &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("case%d envelope Go%s\nSource%s", i, rawJSON(native.Envelope), rawJSON(envelopes[0]))
		}
		var aborted bool
		_ = json.Unmarshal(original.Get("aborted"), &aborted)
		if native.AbortSelf != aborted {
			t.Fatal("actual Source cancellation", native.AbortSelf, aborted)
		}
	}
}
