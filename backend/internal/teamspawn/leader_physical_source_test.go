package teamspawn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"
)

func runLeaderSource(t *testing.T, fixtureName, gate string, rows any) []json.RawMessage {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/" + fixtureName)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), gate + "=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawJSON(map[string]any{"homeDir": home, "rows": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(output))
	}
	var source struct {
		Rows         []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output), stderr.String())
	}
	return source.Rows
}
func compareLeaderSource(t *testing.T, actual []protocol.JSONObject, source []json.RawMessage) {
	t.Helper()
	if len(actual) != len(source) {
		t.Fatal("Source row count", len(actual), len(source))
	}
	for i, row := range actual {
		var a, b any
		_ = json.Unmarshal(rawJSON(row), &a)
		_ = json.Unmarshal(source[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("case%d Go%s\nSource%s", i, rawJSON(row), source[i])
		}
	}
}
func nativeLeaderPhysical(t *testing.T) LeaderInbox {
	t.Helper()
	config := filepath.Join(t.TempDir(), ".claude")
	tasks, err := taskstore.New(taskstore.Options{ConfigDir: config, PID: os.Getpid(), Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	mailbox, err := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: config, TaskStore: tasks})
	if err != nil {
		t.Fatal(err)
	}
	store, err := teamstore.NewConfig(teamstore.ConfigOptions{ConfigDir: config, TaskStore: tasks, PID: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	return LeaderInbox{mailbox, store, tasks}
}

func TestActualLeaderInboxReadMarkStrictApprovalRosterAndTaskCleanupAgainstSource(t *testing.T) {
	message := func(id, from, text string) protocol.JSONObject {
		return inboxObject(rawJSON(map[string]any{"id": id, "from": from, "text": text, "timestamp": "2026-10-07T00:00:00.000Z"}))
	}
	approval := `{"type":"shutdown_approved","requestId":"r","from":"worker","timestamp":"t","backendType":"in-process"}`
	rows := []protocol.JSONObject{}
	for _, messages := range [][]protocol.JSONObject{{}, {message("plain", "worker", "physical completed artifact"), message("idle", "worker", `{"type":"idle_notification"}`), message("permission", "worker", `{"type":"permission_request","agent_id":"worker"}`)}, {message("approved", "worker", approval), message("duplicate", "worker", approval)}, {message("invalid", "worker", `{"type":"shutdown_approved","requestId":"r","from":"worker","timestamp":"t","extra":true}`)}} {
		rows = append(rows, inboxObject(rawJSON(map[string]any{"messages": messages, "members": []map[string]string{{"agentId": "team-lead@team", "name": "team-lead"}, {"agentId": "worker@team", "name": "worker"}, {"agentId": "other@team", "name": "other"}}, "snapshot": map[string]any{"teamName": "team", "leadAgentId": "team-lead@team", "teammates": map[string]any{"worker@team": map[string]any{"name": "worker", "preserved": 1}, "other@team": map[string]string{"name": "other"}}}, "tasks": []map[string]any{{"subject": "actual open work", "description": "full", "status": "in_progress", "owner": "worker@team", "blocks": []string{}, "blockedBy": []string{}}, {"subject": "already done", "description": "full", "status": "completed", "owner": "worker", "blocks": []string{}, "blockedBy": []string{}}, {"subject": "reassigned", "description": "full", "status": "pending", "owner": "other", "blocks": []string{}, "blockedBy": []string{}}}})))
	}
	leader := nativeLeaderPhysical(t)
	actual := []protocol.JSONObject{}
	for i, row := range rows {
		team := fmt.Sprintf("actual-reader-%d", i)
		if err := leader.Config.Write(team, protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quoted(team)}, protocol.JSONField{Name: "createdAt", Value: rawJSON(1)}, protocol.JSONField{Name: "members", Value: row.Get("members")})); err != nil {
			t.Fatal(err)
		}
		var tasks, messages []protocol.JSONObject
		_ = json.Unmarshal(row.Get("tasks"), &tasks)
		_ = json.Unmarshal(row.Get("messages"), &messages)
		for _, task := range tasks {
			if _, err := leader.Tasks.Create(team, task); err != nil {
				t.Fatal(err)
			}
		}
		for _, msg := range messages {
			if err := leader.Mailbox.Write("team-lead", team, msg); err != nil {
				t.Fatal(err)
			}
		}
		snapshot, _ := row.Object("snapshot")
		read, err := leader.Read(team, snapshot)
		if err != nil {
			t.Fatal(err)
		}
		state, err := leader.Tasks.Snapshot(team)
		if err != nil {
			t.Fatal(err)
		}
		formatted := read.Formatted
		if formatted == nil {
			formatted = []byte("null")
		}
		actual = append(actual, protocol.NewJSONObject(protocol.JSONField{Name: "partition", Value: rawJSON(read.Partition)}, protocol.JSONField{Name: "context", Value: rawJSON(read.Snapshot)}, protocol.JSONField{Name: "formatted", Value: formatted}, protocol.JSONField{Name: "mailbox", Value: leader.Mailbox.Read("team-lead", team)}, protocol.JSONField{Name: "members", Value: leader.Config.Read(team).Get("members")}, protocol.JSONField{Name: "tasks", Value: rawJSON(state.Tasks)}, protocol.JSONField{Name: "revision", Value: rawJSON(state.Revision)}))
		again, err := leader.Read(team, read.Snapshot)
		if err != nil || len(again.Partition.Remaining) != 0 {
			t.Fatal("original read marking not retained", again, err)
		}
	}
	compareLeaderSource(t, actual, runLeaderSource(t, "native-team-lead-reader.fixture.ts", "CC_HAHA_LEAD_READER_FIXTURE", rows))
}

func TestActualLeaderPermissionBridgeAllowDenyErrorsSandboxAndNoHostPhysicalResponsesAgainstSource(t *testing.T) {
	messages := []protocol.JSONObject{
		inboxObject(rawJSON(map[string]any{"from": "allow-worker", "text": string(rawJSON(map[string]any{"type": "permission_request", "request_id": "a", "agent_id": "allow-worker", "tool_name": "Write", "tool_use_id": "tool-a", "description": "actual edit", "input": map[string]string{"file_path": "private"}, "permission_suggestions": []any{map[string]string{"type": "addRules"}}}))})),
		inboxObject(rawJSON(map[string]any{"from": "deny-worker", "text": `{"type":"permission_request","request_id":"b","agent_id":"deny-worker","tool_name":"Bash","tool_use_id":"tool-b","input":{},"permission_suggestions":[]}`})),
		inboxObject(rawJSON(map[string]any{"from": "error-worker", "text": `{"type":"permission_request","request_id":"c","agent_id":"error-worker","tool_name":"Read","tool_use_id":"tool-c","input":{}}`})),
		inboxObject(rawJSON(map[string]any{"from": "sandbox-worker", "text": `{"type":"sandbox_permission_request","requestId":"s","workerName":"sandbox-worker","hostPattern":{"host":"loopback.test"}}`})),
		inboxObject(rawJSON(map[string]any{"from": "invalid-sandbox", "text": `{"type":"sandbox_permission_request","requestId":"missing","workerName":"invalid-sandbox"}`})),
	}
	decisions := inboxObject([]byte(`{"allow-worker":{"behavior":"allow","updatedInput":{"actual":true},"updatedPermissions":[{"type":"addRules","destination":"session","rules":[{"toolName":"Write"}],"behavior":"allow"}]},"deny-worker":{"behavior":"deny","message":"actual declined"},"error-worker":{"error":"host transport cancelled"},"sandbox-worker":{"behavior":"allow"}}`))
	rows := []protocol.JSONObject{}
	for _, can := range []bool{true, false} {
		rows = append(rows, inboxObject(rawJSON(map[string]any{"messages": messages, "canPromptHost": can, "decisions": decisions, "recipients": []string{"allow-worker", "deny-worker", "error-worker", "sandbox-worker", "invalid-sandbox"}})))
	}
	leader := nativeLeaderPhysical(t)
	actual := []protocol.JSONObject{}
	for i, row := range rows {
		team := fmt.Sprintf("actual-permission-%d", i)
		asked := []protocol.JSONObject{}
		var mu sync.Mutex
		bridge := LeaderPermissionBridge{Mailbox: leader.Mailbox, TeamName: team, CanPromptHost: string(row.Get("canPromptHost")) == "true", Timestamp: func() string { return "2026-10-07T00:00:00.000Z" }, Ask: func(_ context.Context, request protocol.JSONObject) (protocol.JSONObject, error) {
			mu.Lock()
			asked = append(asked, request.Clone())
			mu.Unlock()
			value, _ := decisions.Object(request.String("displayName"))
			if value.String("error") != "" {
				return protocol.JSONObject{}, errors.New(value.String("error"))
			}
			return value, nil
		}}
		if err := bridge.Resolve(context.Background(), PartitionLeadMailboxMessages(messages)); err != nil {
			t.Fatal(err)
		}
		sort.Slice(asked, func(i, j int) bool { return asked[i].String("displayName") < asked[j].String("displayName") })
		inboxes := protocol.NewJSONObject()
		var recipients []string
		_ = json.Unmarshal(row.Get("recipients"), &recipients)
		for _, name := range recipients {
			var envelope []protocol.JSONObject
			_ = json.Unmarshal(leader.Mailbox.Read(name, team), &envelope)
			for i := range envelope {
				envelope[i].Delete("id")
			}
			if envelope == nil {
				envelope = []protocol.JSONObject{}
			}
			inboxes.Set(name, rawJSON(envelope))
		}
		actual = append(actual, protocol.NewJSONObject(protocol.JSONField{Name: "asked", Value: rawJSON(asked)}, protocol.JSONField{Name: "inboxes", Value: rawJSON(inboxes)}))
	}
	compareLeaderSource(t, actual, runLeaderSource(t, "native-team-lead-permission.fixture.ts", "CC_HAHA_LEAD_PERMISSION_FIXTURE", rows))
}
