package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

func TestActualLeaderSendMessageAndBroadcastPhysicalMailboxMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source required")
	}
	rows := []map[string]any{
		{"team": false, "input": map[string]string{"to": "worker", "message": "default mailbox", "summary": "default"}, "recipients": []string{"worker"}},
		{"team": false, "input": map[string]string{"to": "*", "message": "missing context", "summary": "missing"}},
		{"team": true, "members": []any{map[string]string{"name": "TEAM-LEAD"}}, "input": map[string]string{"to": "*", "message": "nobody else", "summary": "self"}},
		{"team": true, "members": []any{map[string]string{"name": "TEAM-LEAD"}, map[string]string{"name": "one"}, map[string]string{"name": "two"}}, "input": map[string]string{"to": "*", "message": "real broadcast", "summary": "both"}, "recipients": []string{"one", "two"}},
		{"team": true, "members": []any{map[string]string{"name": "team-lead"}, map[string]string{"name": "one"}}, "teammates": map[string]any{"one@team": map[string]string{"name": "one", "color": "blue"}}, "input": map[string]string{"to": "one", "message": "color routing", "summary": "direct"}, "recipients": []string{"one"}},
	}
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		n.Environment["CC_HAHA_TEAM_REVIEW_REQUIRED"] = "1"
	}, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"SendMessage,Read"} })
	actual := []map[string]any{}
	for i, row := range rows {
		name := ""
		if row["team"] == true {
			name = fmt.Sprintf("actual-team-%d", i)
			if err := m.teamLeader.config.Write(name, objMain(rawMain(map[string]any{"name": name, "leadSessionId": "actual", "createdAt": 1, "members": row["members"]}))); err != nil {
				t.Fatal(err)
			}
		}
		var data any
		var failure any
		if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			m.teamState = nil
			if name != "" {
				teammates := row["teammates"]
				if teammates == nil {
					teammates = map[string]any{}
				}
				m.teamState = &nativeTeamState{snapshot: objMain(rawMain(map[string]any{"teamName": name, "teammates": teammates}))}
			}
			input := objMain(rawMain(row["input"]))
			future, err := m.sendNativeTeamMessage(context.Background(), scope, input)
			if err != nil {
				failure = err.Error()
				return nil
			}
			result, err := future.Await(scope)
			if err != nil {
				failure = err.Error()
				return nil
			}
			data = json.RawMessage(result.Data)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		inboxes := map[string]any{}
		mailbox, err := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: m.options.Native.Environment["CLAUDE_CONFIG_DIR"], TaskStore: m.taskStore})
		if err != nil {
			t.Fatal(err)
		}
		if recipients, ok := row["recipients"].([]string); ok {
			for _, recipient := range recipients {
				var messages []protocol.JSONObject
				_ = json.Unmarshal(mailbox.Read(recipient, name), &messages)
				for i := range messages {
					messages[i].Delete("id")
					messages[i].Delete("timestamp")
				}
				inboxes[recipient] = messages
			}
		}
		actual = append(actual, map[string]any{"data": data, "error": failure, "inboxes": inboxes})
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-leader-mailbox.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_LEADER_MAILBOX_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"homeDir": home, "rows": rows}))
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
	if err = json.Unmarshal(output, &source); err != nil || len(source.Rows) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output), stderr.String())
	}
	for i, row := range actual {
		var a, b any
		_ = json.Unmarshal(rawMain(row), &a)
		_ = json.Unmarshal(source.Rows[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("case%d Go%s Source%s", i, rawMain(row), source.Rows[i])
		}
	}
}
