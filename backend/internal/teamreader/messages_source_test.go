package teamreader

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestActualWorkbenchMailboxBroadcastOccurrenceProtocolAndUTF16MatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	files := []map[string]string{
		{"path": ".claude/teams/team/inboxes/a.json", "content": `[{"id":"shared","from":"worker","text":"same","timestamp":"2026-10-07T00:00:01.999Z","summary":"summary","color":"blue","read":false},{"from":"legacy","text":"repeat","timestamp":"2026-10-07T00:00:02.001Z"},{"from":"legacy","text":"repeat","timestamp":"2026-10-07T00:00:02.002Z"},{"from":"worker","text":"{\"type\":\"task_update\",\"taskId\":\"1\",\"subject\":\"Original subject\"}","timestamp":"2026-10-07T00:00:03.000Z"},null,{},false,{"from":"\ud800","text":"\udc00","timestamp":"2026-10-07T00:00:04.000Z"}]`},
		{"path": ".claude/teams/team/inboxes/b.json", "content": `[{"id":"shared","from":"worker","text":"same","timestamp":"2026-10-07T00:00:01.100Z","summary":"summary","color":"blue","read":true},{"from":"legacy","text":"repeat","timestamp":"2026-10-07T00:00:02.010Z"},{"from":"worker","text":"{\"type\":\"task_update\",\"taskId\":\"1\",\"subject\":\"Original subject\"}","timestamp":"2026-10-07T00:00:03.000Z"},{"from":"worker","text":"{\"type\":\"shutdown_request\",\"reason\":\"Original reason\"}","timestamp":"2026-10-07T00:00:05.000Z"}]`},
		{"path": ".claude/teams/team/inboxes/c.json", "content": `{"not":"an array"}`}, {"path": ".claude/teams/team/inboxes/corrupt.json", "content": `bad JSON`},
	}
	rowsInput := []map[string]string{{"method": "messages", "name": "team"}, {"method": "messages", "name": "missing"}}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-reader-basic.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_READER_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "files": files, "rows": rowsInput, "now": 20000}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Rows         []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Rows) != 2 || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output))
	}
	root := t.TempDir()
	for _, file := range files {
		path := filepath.Join(root, file["path"])
		_ = os.MkdirAll(filepath.Dir(path), 0700)
		if err = os.WriteFile(path, []byte(file["content"]), 0600); err != nil {
			t.Fatal(err)
		}
	}
	config := filepath.Join(root, ".claude")
	tasks, _ := taskstore.New(taskstore.Options{ConfigDir: config, PID: os.Getpid(), Now: time.Now})
	mailbox, _ := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: config, TaskStore: tasks})
	reader, _ := New(Options{ConfigDir: config, Tasks: tasks, Mailbox: mailbox, Now: time.Now})
	for i, row := range rowsInput {
		data, err := reader.ReadWorkbenchMessages(row["name"])
		if err != nil {
			t.Fatal(err)
		}
		actual := raw(map[string]any{"data": data, "status": 200, "error": nil, "inbox": nil})
		var a, b any
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(source.Rows[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("row %d Go %s Source %s", i, actual, source.Rows[i])
		}
	}
	// The read-only workbench producer must preserve physical unread markers.
	for _, file := range files {
		actual, err := os.ReadFile(filepath.Join(root, file["path"]))
		if err != nil || string(actual) != file["content"] {
			t.Fatal("read mutated a mailbox", err)
		}
	}
}
