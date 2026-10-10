package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/attachments"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func runInteractiveInboxSource(t *testing.T, rows, deliveries any) (json.RawMessage, json.RawMessage) {
	return runInteractiveInboxSourceWithOptions(t, rows, deliveries, nil)
}

func runInteractiveInboxSourceWithOptions(t *testing.T, rows, deliveries any, extra map[string]any) (json.RawMessage, json.RawMessage) {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	if extra["realPaneBinary"] != nil {
		short, err := os.MkdirTemp("/tmp", "team-source-")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(short)
		home, _ = filepath.EvalSymlinks(short)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-interactive-inbox.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_INTERACTIVE_INBOX_FIXTURE=1", "CC_HAHA_AGENT_TEAMS_ENABLED=1", "CLAUDE_CODE_ENABLE_TASKS=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	input := map[string]any{"homeDir": home, "rows": rows, "deliveries": deliveries}
	for key, value := range extra {
		input[key] = value
	}
	cmd.Stdin = bytes.NewReader(rawMain(input))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(out))
	}
	var source struct {
		Rows, Deliveries json.RawMessage
		Capabilities     struct{ Blocked []string }
	}
	if err := json.Unmarshal(out, &source); err != nil || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(out), stderr.String())
	}
	return source.Rows, source.Deliveries
}

func cleanInteractiveMessages(rows []protocol.JSONObject) []protocol.JSONObject {
	out := []protocol.JSONObject{}
	for _, row := range rows {
		row = row.Clone()
		row.Delete("id")
		out = append(out, row)
	}
	return out
}

func TestActualInteractiveLeaderPollPermissionsPlanTrustedShutdownAndPendingQueueMatchSource(t *testing.T) {
	message := func(from, text string) protocol.JSONObject {
		return objMain(rawMain(map[string]any{"from": from, "text": text, "timestamp": "t", "color": "blue", "summary": "actual summary"}))
	}
	members := []map[string]any{{"agentId": "team-lead@team", "name": "team-lead", "tmuxPaneId": "", "backendType": "in-process"}, {"agentId": "worker@team", "name": "worker", "tmuxPaneId": "", "backendType": "in-process"}}
	roster := map[string]any{"team-lead@team": map[string]any{"name": "team-lead"}, "worker@team": map[string]any{"name": "worker", "preserved": true}}
	permission := `{"type":"permission_request","request_id":"permission","agent_id":"worker","tool_name":"Write","tool_use_id":"actual-write","description":"actual edit","input":{"content":"private"},"permission_suggestions":[],"unknown":true}`
	approved := `{"type":"shutdown_approved","requestId":"shutdown","from":"worker","timestamp":"t","paneId":"%untrusted","backendType":"tmux"}`
	plan := `{"type":"plan_approval_request","requestId":"plan","from":"worker","timestamp":"t","planFilePath":"/private/plan","planContent":"actual plan","unknown":true}`
	rows := []map[string]any{
		{"messages": []protocol.JSONObject{message("worker", "ACTUAL_SOURCE_CHAT"), message("worker", permission), message("worker", `{"type":"permission_response","request_id":"p","subtype":"error","error":"no"}`), message("worker", `{"type":"permission_response","request_id":"p","subtype":"error","error":"no","extra":true}`)}, "members": members, "teammates": roster, "recipients": []string{"worker"}, "resolvePermissions": true},
		{"messages": []protocol.JSONObject{message("worker", approved), message("worker", plan), message("attacker", approved), message("worker", "actual chat")}, "members": members, "teammates": roster, "recipients": []string{"worker"}, "mode": "plan", "tasks": []map[string]any{{"subject": "actual open work", "description": "full", "status": "in_progress", "owner": "worker@team", "blocks": []string{}, "blockedBy": []string{}}}},
		{"messages": []protocol.JSONObject{message("worker", approved), message("worker", `{"type":"shutdown_approved","requestId":"s","from":"worker","timestamp":"t","extra":true}`)}, "members": members, "teammates": roster, "missingConfig": true, "recipients": []string{}},
	}
	actual := []map[string]any{}
	for i, row := range rows {
		asked := []map[string]any{}
		m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) { n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1" }, func(o *MainPrintOptions) {
			o.Interactive = true
			o.Arguments.Tools = []string{"TeamCreate,TeamDelete,Read,Write,SendMessage"}
			o.ResolvePermission = func(_ context.Context, _ *cooperative.Scope, tool queryloop.RegisteredTool, input *protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
				asked = append(asked, map[string]any{"toolName": tool.Name, "toolUseId": call.ToolUseID, "displayName": call.PermDecision.DisplayName, "input": input.Clone()})
				return queryloop.ToolPermission{Behavior: "allow", UpdatedInput: input, UpdatedPermissions: rawMain([]any{})}, nil
			}
		})
		team := fmt.Sprintf("actual-interactive-%d", i)
		callActualTeamTool(t, m, "TeamCreate", string(rawMain(map[string]string{"team_name": team})))
		if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			m.teamState = &nativeTeamState{snapshot: objMain(rawMain(map[string]any{"teamName": team, "leadAgentId": "team-lead@team", "teammates": roster}))}
			if row["missingConfig"] == true {
				if err := os.Remove(m.teamLeader.config.Path(team)); err != nil {
					return err
				}
			} else {
				if err := m.teamLeader.config.Write(team, objMain(rawMain(map[string]any{"name": team, "createdAt": 1, "members": members}))); err != nil {
					return err
				}
			}
			if mode, ok := row["mode"].(string); ok {
				if err := m.Host.SetNativePermissionMode(scope, mode); err != nil {
					return err
				}
			} else {
				if err := m.Host.SetNativePermissionMode(scope, "default"); err != nil {
					return err
				}
			}
			var err error
			m.teamLeader.mailbox, err = teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: m.options.Native.Environment["CLAUDE_CONFIG_DIR"], TaskStore: m.taskStore})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if list, ok := row["tasks"].([]map[string]any); ok {
			for _, task := range list {
				if _, err := m.taskStore.Create(team, objMain(rawMain(task))); err != nil {
					t.Fatal(err)
				}
			}
		}
		for _, message := range row["messages"].([]protocol.JSONObject) {
			if err := m.teamLeader.mailbox.Write("team-lead", team, message); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := m.PollInteractiveLeaderInbox(context.Background()); err != nil {
			t.Fatal(err)
		}
		// Join the physical permission response while owner-native completions pump.
		if row["resolvePermissions"] == true {
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				if string(m.teamLeader.mailbox.Read("worker", team)) != "[]" {
					break
				}
				time.Sleep(time.Millisecond)
			}
		}
		if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			regular := cleanInteractiveMessages(m.Controller.AppState().Inbox)
			for i := range regular {
				if regular[i].String("from") == "system" {
					regular[i].Delete("timestamp")
				}
			}
			inboxes := map[string]any{}
			for _, recipient := range row["recipients"].([]string) {
				var envelopes []protocol.JSONObject
				_ = json.Unmarshal(m.teamLeader.mailbox.Read(recipient, team), &envelopes)
				envelopes = cleanInteractiveMessages(envelopes)
				for i := range envelopes {
					envelopes[i].Delete("timestamp")
					body := objMain([]byte(envelopes[i].String("text")))
					if body.IsObject() {
						body.Delete("timestamp")
						envelopes[i].Set("text", quoteMain(string(rawMain(body))))
					}
				}
				inboxes[recipient] = envelopes
			}
			var mailbox []protocol.JSONObject
			_ = json.Unmarshal(m.teamLeader.mailbox.Read("team-lead", team), &mailbox)
			members := any(nil)
			if config := m.teamLeader.config.ReadAsync(team); config != nil {
				members = config.Get("members")
			}
			snapshot, err := m.taskStore.Snapshot(team)
			if err != nil {
				return err
			}
			actual = append(actual, map[string]any{"regular": regular, "teammates": m.teamState.snapshot.Get("teammates"), "members": members, "tasks": snapshot.Tasks, "revision": snapshot.Revision, "mailbox": cleanInteractiveMessages(mailbox), "inboxes": inboxes, "permissions": asked})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	want, _ := runInteractiveInboxSource(t, rows, []any{})
	var a, b any
	_ = json.Unmarshal(rawMain(actual), &a)
	_ = json.Unmarshal(want, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("Go%s\nSource%s", rawMain(actual), want)
	}
}

func TestActualInteractiveMailboxAttachmentDedupProcessedDeliveryAndAPIProjectionMatchSource(t *testing.T) {
	message := func(from, text, stamp string) protocol.JSONObject {
		return objMain(rawMain(map[string]any{"from": from, "text": text, "timestamp": stamp, "color": "blue", "summary": "actual summary"}))
	}
	duplicate := message("worker", "ACTUAL_MID_TURN", "t")
	pending := duplicate.Clone()
	pending.Set("id", quoteMain("pending"))
	pending.Set("status", quoteMain("pending"))
	processed := message("other", "already delivered", "old")
	processed.Set("id", quoteMain("processed"))
	processed.Set("status", quoteMain("processed"))
	rows := []map[string]any{{"messages": []protocol.JSONObject{duplicate, message("worker", `{"type":"permission_request","unknown":true}`, "control"), message("worker", `{"type":"idle_notification","from":"worker","value":1}`, "idle1"), message("worker", `{"type":"idle_notification","from":"worker","value":2}`, "idle2")}, "pending": []protocol.JSONObject{pending, processed}}}
	rows = append(rows, map[string]any{"messages": []protocol.JSONObject{message("leader-peer", "PRIVATE_LEADER_FILE", "leader")}, "pending": []protocol.JSONObject{pending}, "viewedTask": objMain(rawMain(map[string]any{"id": "actual-viewed-task", "type": "in_process_teammate", "status": "running", "identity": map[string]string{"agentName": "viewed-worker"}})), "viewedMessages": []protocol.JSONObject{message("other-peer", "ACTUAL_VIEWED_MAILBOX", "viewed"), message("team-lead", `{"type":"permission_response","request_id":"view-control","subtype":"error","error":"no"}`, "control")}})
	actual := []map[string]any{}
	for index, row := range rows {
		m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
			n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
			delete(n.Environment, "CLAUDE_CODE_DISABLE_ATTACHMENTS")
		}, func(o *MainPrintOptions) {
			o.Interactive = true
			o.Arguments.Tools = []string{"TeamCreate,TeamDelete,Read,SendMessage"}
		})
		team := fmt.Sprintf("actual-attachment-%d", index)
		callActualTeamTool(t, m, "TeamCreate", string(rawMain(map[string]string{"team_name": team})))
		if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			m.options.Native.Environment["USER_TYPE"] = "ant"
			var err error
			m.teamLeader.mailbox, err = teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: m.options.Native.Environment["CLAUDE_CONFIG_DIR"], TaskStore: m.taskStore})
			if err != nil {
				return err
			}
			for _, message := range row["messages"].([]protocol.JSONObject) {
				if err := m.teamLeader.mailbox.Write("team-lead", team, message); err != nil {
					return err
				}
			}
			if task, ok := row["viewedTask"].(protocol.JSONObject); ok {
				identity, _ := task.Object("identity")
				_, cancel := context.WithCancelCause(context.Background())
				if err := m.Controller.RegisterBackgroundTaskOnOwner(scope, &sessioncontroller.BackgroundTask{Data: task, Cancel: cancel, ReadOutput: func(context.Context) (json.RawMessage, error) { return rawMain([]any{}), nil }, Join: func(context.Context) error { return nil }}); err != nil {
					return err
				}
				if err := m.Controller.EnterAgentViewOnOwner(scope, task.String("id"), m.options.Native.NowMillis()); err != nil {
					return err
				}
				for _, message := range row["viewedMessages"].([]protocol.JSONObject) {
					if err := m.teamLeader.mailbox.Write(identity.String("agentName"), team, message); err != nil {
						return err
					}
				}
			}
			if err := m.Controller.QueueTeamInboxOnOwner(scope, row["pending"].([]protocol.JSONObject)); err != nil {
				return err
			}
			before := m.Controller.AppState().Inbox
			if err := m.Controller.ProcessTeamInboxOnOwner(scope, before[1:]); err != nil {
				return err
			}
			row["pending"] = m.Controller.AppState().Inbox
			got, err := m.nativeTeamMailboxAttachments(scope, "")
			if err != nil {
				return err
			}
			payloads := []protocol.JSONObject{}
			normalized := []map[string]any{}
			for _, message := range got {
				payload := objMain(message.Record.Get("attachment"))
				payloads = append(payloads, payload)
				users, err := attachments.Project(rawMain(payload), m.factory)
				if err != nil {
					return err
				}
				for _, user := range users {
					body, _ := user.Object("message")
					normalized = append(normalized, map[string]any{"content": body.Get("content"), "isMeta": user.Get("isMeta")})
				}
			}
			var mailbox []protocol.JSONObject
			_ = json.Unmarshal(m.teamLeader.mailbox.Read("team-lead", team), &mailbox)
			observation := map[string]any{"attachments": payloads, "pending": m.Controller.AppState().Inbox, "mailbox": cleanInteractiveMessages(mailbox), "normalized": normalized}
			if task, ok := row["viewedTask"].(protocol.JSONObject); ok {
				identity, _ := task.Object("identity")
				var viewed []protocol.JSONObject
				_ = json.Unmarshal(m.teamLeader.mailbox.Read(identity.String("agentName"), team), &viewed)
				observation["viewedMailbox"] = cleanInteractiveMessages(viewed)
			}
			actual = append(actual, observation)
			again, err := m.nativeTeamMailboxAttachments(scope, "")
			if err != nil || len(again) != 0 {
				return fmt.Errorf("duplicate attachment delivery %d %v", len(again), err)
			}
			if _, viewed := row["viewedTask"]; viewed {
				if len(m.Controller.AppState().Inbox) != 1 || m.Controller.AppState().Inbox[0].String("status") != "pending" {
					return fmt.Errorf("viewed teammate consumed leader pending inbox")
				}
				return nil
			}
			if queued, err := m.enqueueInteractiveLeaderInbox(scope); err != nil || queued {
				return fmt.Errorf("processed messages resubmitted %v %v", queued, err)
			}
			if len(m.Controller.AppState().Inbox) != 0 {
				return fmt.Errorf("processed inbox not cleared at idle")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	_, want := runInteractiveInboxSource(t, []any{}, rows)
	var a, b any
	_ = json.Unmarshal(rawMain(actual), &a)
	_ = json.Unmarshal(want, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("Go%s\nSource%s", rawMain(actual), want)
	}
}
