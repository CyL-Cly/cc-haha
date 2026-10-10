package teamreader

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestActualWorkbenchLockedDAGRosterFilterUnreadAndArchiveMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	files := []map[string]string{{"path": ".claude/teams/team/config.json", "content": `{"name":"team","createdAt":1000,"leadAgentId":"team-lead@team","leadSessionId":"parent","members":[{"agentId":"team-lead@team","name":"team-lead","joinedAt":1000,"cwd":"/fixture","isActive":false},{"agentId":"member@team","name":"member","joinedAt":1000,"cwd":"/fixture","isActive":true}]}`}, {"path": ".claude/teams/team/inboxes/unregistered.json", "content": `[{"id":"m","from":"member","text":"Original unread message","timestamp":"1970-01-01T00:00:10.000Z","read":false}]`}}
	taskValues := []protocol.JSONObject{obj([]byte(`{"subject":"Visible","description":"actual","status":"pending","owner":"member","blocks":[],"blockedBy":[],"future":{"keep":true}}`)), obj([]byte(`{"subject":"Hidden","description":"internal","status":"pending","blocks":[],"blockedBy":[],"metadata":{"_internal":true}}`))}
	seed := []map[string]any{}
	for _, task := range taskValues {
		seed = append(seed, map[string]any{"team": "team", "value": task})
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-reader-basic.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_READER_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "USER_TYPE=external", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "files": files, "tasks": seed, "rows": []map[string]string{{"method": "workbench", "name": "team"}}, "now": 20000}))
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
	if err = json.Unmarshal(output, &source); err != nil || len(source.Rows) != 1 || len(source.Capabilities.Blocked) > 0 {
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
	now := func() time.Time { return time.UnixMilli(20000) }
	tasks, _ := taskstore.New(taskstore.Options{ConfigDir: config, PID: os.Getpid(), Now: now})
	for _, task := range taskValues {
		if _, err = tasks.Create("team", task); err != nil {
			t.Fatal(err)
		}
	}
	mailbox, _ := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: config, TaskStore: tasks})
	reader, _ := New(Options{ConfigDir: config, Tasks: tasks, Mailbox: mailbox, Now: now})
	data, err := reader.GetWorkbench(context.Background(), "team")
	if err != nil {
		t.Fatal(err)
	}
	actual := raw(map[string]any{"data": data, "status": 200, "error": nil, "inbox": nil})
	var a, b any
	_ = json.Unmarshal(actual, &a)
	_ = json.Unmarshal(source.Rows[0], &b)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("Go %s Source %s", actual, source.Rows[0])
	}
	doc := reader.ReadArchive("parent")
	if doc == nil {
		t.Fatal("actual archive not written")
	}
	entry := reader.SelectArchiveEntry(doc, protocol.NewJSONObject())
	if entry == nil || lastSnapshot(*entry).String("version") != data.String("version") {
		t.Fatal("archive version", entry, data)
	}
}
