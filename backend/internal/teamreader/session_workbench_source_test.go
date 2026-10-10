package teamreader

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

func TestActualSessionWorkbenchLiveArchiveLegacyMigrationLookupAndIncompletePreservationMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	live := "aaaaaaaa-aaaa-4aaa-8aaa-000000000001"
	old := "aaaaaaaa-aaaa-4aaa-8aaa-000000000002"
	legacy := "aaaaaaaa-aaaa-4aaa-8aaa-000000000003"
	partial := "aaaaaaaa-aaaa-4aaa-8aaa-000000000004"
	config := protocol.NewJSONObject(field("name", "live-team"), field("createdAt", 1000), field("leadSessionId", live), field("leadAgentId", "team-lead@live-team"), field("members", []protocol.JSONObject{protocol.NewJSONObject(field("agentId", "team-lead@live-team"), field("name", "team-lead"), field("joinedAt", 1000), field("cwd", "/actual/workspace"), field("isActive", false))}))
	create := `{"type":"assistant","uuid":"create","timestamp":"1970-01-01T00:00:01.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"create","name":"TeamCreate","input":{"team_name":"migrated","description":"Legacy actual"}}]}}` + "\n"
	result := `{"type":"user","uuid":"created","timestamp":"1970-01-01T00:00:02.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"create","content":"created"}]},"toolUseResult":{"team_name":"migrated","lead_agent_id":"team-lead@migrated"}}` + "\n"
	deleteTool := `{"type":"assistant","uuid":"delete","timestamp":"1970-01-01T00:00:03.000Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"delete","name":"TeamDelete","input":{}}]}}` + "\n"
	files := []map[string]string{{"path": ".claude/teams/live-team/config.json", "content": string(raw(config))}, {"path": ".claude/projects/p/" + legacy + ".jsonl", "content": create + result + deleteTool}, {"path": ".claude/projects/p/" + partial + ".jsonl", "content": create + `{"type":"user"`}, {"path": ".claude/tasks/live-team/1.json", "content": `{"id":"1","subject":"Actual task","description":"","status":"pending","blocks":[],"blockedBy":[]}`}}
	oldTeam := protocol.NewJSONObject(field("name", "archived"), field("createdAt", 1000), field("leadSessionId", old), field("members", []any{}))
	oldTeam.Set("incarnationId", raw(incarnation(oldTeam)))
	snapshot := protocol.NewJSONObject(field("version", "terminal"), field("generatedAt", "1970-01-01T00:00:05.000Z"), field("deletedAt", "1970-01-01T00:00:05.000Z"), field("terminalTaskFrameId", "durable-final"), field("team", oldTeam), field("tasks", []any{}), field("messages", []any{}))
	input := []protocol.JSONObject{protocol.NewJSONObject(field("session", live), field("timeline", protocol.NewJSONObject())), protocol.NewJSONObject(field("session", live), field("timeline", protocol.NewJSONObject(field("teamName", "missing")))), protocol.NewJSONObject(field("session", old), field("snapshots", []protocol.JSONObject{snapshot}), field("timeline", protocol.NewJSONObject())), protocol.NewJSONObject(field("session", legacy), field("timeline", protocol.NewJSONObject())), protocol.NewJSONObject(field("session", legacy), field("timeline", protocol.NewJSONObject(field("at", 2000)))), protocol.NewJSONObject(field("session", partial), field("timeline", protocol.NewJSONObject())), protocol.NewJSONObject(field("session", "aaaaaaaa-aaaa-4aaa-8aaa-000000000099"), field("timeline", protocol.NewJSONObject()))}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-archive.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_ARCHIVE_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "files": files, "rows": input, "now": 20000}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Observations []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Observations) != len(input) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output))
	}
	root := t.TempDir()
	directory := filepath.Join(root, ".claude")
	for _, file := range files {
		path := filepath.Join(root, file["path"])
		_ = os.MkdirAll(filepath.Dir(path), 0700)
		if err = os.WriteFile(path, []byte(file["content"]), 0600); err != nil {
			t.Fatal(err)
		}
	}
	now := func() time.Time { return time.UnixMilli(20000) }
	tasks, _ := taskstore.New(taskstore.Options{ConfigDir: directory, PID: os.Getpid(), Now: now})
	mailbox, _ := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: directory, TaskStore: tasks})
	reader, _ := New(Options{ConfigDir: directory, Tasks: tasks, Mailbox: mailbox, Now: now, HistoryBudget: sessionstore.NewBudget()})
	for i, row := range input {
		session := row.String("session")
		for _, snapshot := range rows(row.Get("snapshots")) {
			if err = reader.ArchiveSnapshot(context.Background(), snapshot); err != nil {
				t.Fatal(err)
			}
		}
		value, err := reader.GetWorkbenchForSession(context.Background(), session, obj(row.Get("timeline")))
		var failure any
		if err != nil {
			failure = err.Error()
		}
		actual := raw(map[string]any{"value": value, "error": failure})
		var a, b any
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(source.Observations[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("case %d Go %s Source %s", i, actual, source.Observations[i])
		}
	}
}
