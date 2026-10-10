package teamspawn

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestInProcessWaitPriorityAndPhysicalTaskClaimsAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	msg := func(from, text string, read bool) map[string]any {
		return map[string]any{"from": from, "text": text, "read": read, "timestamp": "2026-10-07T00:00:00.000Z"}
	}
	task := func(status, owner string) map[string]any {
		return map[string]any{"subject": "actual follow-up", "description": "physical claim", "status": status, "owner": owner, "blocks": []string{}, "blockedBy": []string{}}
	}
	shutdown := `{"type":"shutdown_request","requestId":"actual-request","from":"team-lead","timestamp":"actual","reason":"stop","unknown":"strip"}`
	rows := []map[string]any{
		{"pending": []string{"user first", "still queued"}, "mailbox": []any{msg("team-lead", shutdown, false)}, "tasks": []any{}},
		{"pending": []string{""}, "mailbox": []any{}, "tasks": []any{}},
		{"mailbox": []any{msg("peer", "peer first", false), msg("team-lead", "leader second", false)}, "tasks": []any{}},
		{"mailbox": []any{msg("team-lead", "leader", false), msg("peer", shutdown, false)}, "tasks": []any{}},
		{"mailbox": []any{msg("team-lead", "read", true), msg("peer", "fifo", false)}, "tasks": []any{}},
		{"mailbox": []any{msg("peer", `{"type":"shutdown_request"}`, false), msg("team-lead", "valid leader", false)}, "tasks": []any{}},
		{"mailbox": []any{}, "tasks": []any{task("completed", "worker"), task("pending", "")}},
		{"abort": true, "pending": []string{"not consumed"}, "mailbox": []any{msg("team-lead", "not read", false)}, "tasks": []any{}},
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	root := t.TempDir()
	config := filepath.Join(root, ".claude")
	store, err := taskstore.New(taskstore.Options{ConfigDir: config, PID: os.Getpid(), Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	mailbox, err := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: config, TaskStore: store})
	if err != nil {
		t.Fatal(err)
	}
	actual := []map[string]any{}
	for i, row := range rows {
		team := fmt.Sprintf("Inbox-%d", i)
		path := mailbox.Path("worker", team)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, rawJSON(row["mailbox"]), 0600); err != nil {
			t.Fatal(err)
		}
		for _, value := range row["tasks"].([]any) {
			var parsed protocol.JSONObject
			_ = parsed.UnmarshalJSON(rawJSON(value))
			if _, err := store.Create(teamstore.SanitizeName(team), parsed); err != nil {
				t.Fatal(err)
			}
		}
		pending := []string{}
		if p, ok := row["pending"]; ok {
			pending = append(pending, p.([]string)...)
		}
		inbox, err := NewInbox(InboxOptions{Mailbox: mailbox, Tasks: store, TeamName: team, AgentName: "worker", ReadPending: func(context.Context) (string, bool, error) {
			if len(pending) == 0 {
				return "", false, nil
			}
			message := pending[0]
			pending = pending[1:]
			return message, true, nil
		}, Log: func(err error) { t.Error(err) }})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if row["abort"] == true {
			cancel()
		}
		result, err := inbox.Wait(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		tasks, err := store.List(teamstore.SanitizeName(team))
		if err != nil {
			t.Fatal(err)
		}
		actual = append(actual, map[string]any{"result": result, "pending": pending, "mailbox": mailbox.Read("worker", team), "tasks": tasks})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-inprocess-teammate-inbox.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_INPROCESS_TEAM_INBOX_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
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
