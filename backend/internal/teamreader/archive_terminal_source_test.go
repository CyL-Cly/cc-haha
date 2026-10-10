package teamreader

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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

func TestActualArchiveTombstoneReplayRevisionCausalBoundaryTerminalReceiptAndIdempotenceMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	files := []map[string]string{}
	input := []protocol.JSONObject{}
	task := func(id, subject, status string) protocol.JSONObject {
		return protocol.NewJSONObject(field("id", id), field("subject", subject), field("description", ""), field("status", status), field("blocks", []string{}), field("blockedBy", []string{}))
	}
	tool := func(id, name string, input protocol.JSONObject, stamp int) string {
		return string(raw(protocol.NewJSONObject(field("type", "assistant"), field("uuid", id), field("timestamp", time.Unix(int64(stamp), 0).UTC().Format("2006-01-02T15:04:05.000Z")), field("message", protocol.NewJSONObject(field("role", "assistant"), field("content", []protocol.JSONObject{protocol.NewJSONObject(field("type", "tool_use"), field("id", id), field("name", name), field("input", input))})))))) + "\n"
	}
	result := func(id string, value protocol.JSONObject, stamp int) string {
		return string(raw(protocol.NewJSONObject(field("type", "user"), field("uuid", id+"-result"), field("timestamp", time.Unix(int64(stamp), 0).UTC().Format("2006-01-02T15:04:05.000Z")), field("message", protocol.NewJSONObject(field("role", "user"), field("content", []protocol.JSONObject{protocol.NewJSONObject(field("type", "tool_result"), field("tool_use_id", id), field("content", "Actual result"))}))), field("toolUseResult", value)))) + "\n"
	}
	for i := 0; i < 7; i++ {
		session := fmt.Sprintf("aaaaaaaa-aaaa-4aaa-8aaa-%012d", i)
		name := fmt.Sprintf("team-%d", i)
		team := protocol.NewJSONObject(field("name", name), field("createdAt", 1000), field("leadSessionId", session), field("leadAgentId", "lead@"+name), field("activeMemberCount", 1), field("memberCount", 1), field("members", []protocol.JSONObject{protocol.NewJSONObject(field("agentId", "lead@"+name), field("name", "team-lead"), field("status", "failed"), field("activity", "active"))}))
		id := incarnation(team)
		team.Set("incarnationId", raw(id))
		base := protocol.NewJSONObject(field("version", "base"), field("generatedAt", "1970-01-01T00:00:05.000Z"), field("taskListRevision", 1), field("team", team), field("tasks", []protocol.JSONObject{task("1", "original", "pending")}), field("messages", []any{}), field("futureSnapshot", "keep"))
		history := tool("create", "TeamCreate", protocol.NewJSONObject(field("team_name", name)), 1) + result("create", protocol.NewJSONObject(field("team_name", name)), 2)
		switch i {
		case 1, 4, 5:
			history += tool("update", "TaskUpdate", protocol.NewJSONObject(field("taskId", "1"), field("status", "completed"), field("owner", "worker")), 6) + result("update", protocol.NewJSONObject(field("success", true), field("taskListMutationRevision", 2)), 7)
		case 2:
			history += tool("cross", "TaskUpdate", protocol.NewJSONObject(field("taskId", "1"), field("status", "completed")), 4) + result("cross", protocol.NewJSONObject(field("taskListMutationAt", "1970-01-01T00:00:06.000Z"), field("taskListMutationRevision", 2)), 7)
		case 3:
			history += tool("delete", "TeamDelete", protocol.NewJSONObject(), 3) + result("delete", protocol.NewJSONObject(field("finalTasks", []protocol.JSONObject{task("2", "terminal exact", "completed")}), field("taskListSnapshotRevision", 1)), 4)
		case 6:
			history += tool("cross", "TaskUpdate", protocol.NewJSONObject(field("taskId", "1"), field("status", "completed")), 4) + result("cross", protocol.NewJSONObject(field("success", true)), 7)
		}
		files = append(files, map[string]string{"path": ".claude/projects/p/" + session + ".jsonl", "content": history})
		if i == 4 || i == 5 {
			identity := protocol.NewJSONObject(field("teamName", name), field("createdAt", 1000), field("leadSessionId", session))
			if i == 5 {
				identity.Set("createdAt", raw(2000))
			}
			receipt := protocol.NewJSONObject(field("frameId", "terminal-"+name), field("generation", 1), field("identity", identity), field("tasks", []protocol.JSONObject{task("3", "lifecycle terminal", "completed")}), field("capturedAt", "1970-01-01T00:00:10.000Z"), field("revision", 5))
			state := protocol.NewJSONObject(field("schemaVersion", 1), field("generation", 1), field("deleted", true), field("terminals", []protocol.JSONObject{receipt}))
			files = append(files, map[string]string{"path": ".claude/tasks/.lifecycle-locks/" + name + ".state.json", "content": string(raw(state))})
		}
		input = append(input, protocol.NewJSONObject(field("session", session), field("snapshots", []protocol.JSONObject{base}), field("mark", protocol.NewJSONObject(field("team", name), field("incarnation", id)))))
		input = append(input, protocol.NewJSONObject(field("session", session), field("mark", protocol.NewJSONObject(field("team", name), field("incarnation", id)))))
	}
	home := t.TempDir()
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
	config := filepath.Join(root, ".claude")
	for _, file := range files {
		path := filepath.Join(root, file["path"])
		_ = os.MkdirAll(filepath.Dir(path), 0700)
		if err = os.WriteFile(path, []byte(file["content"]), 0600); err != nil {
			t.Fatal(err)
		}
	}
	tasks, _ := taskstore.New(taskstore.Options{ConfigDir: config, PID: os.Getpid(), Now: time.Now})
	mailbox, _ := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: config, TaskStore: tasks})
	reader, _ := New(Options{ConfigDir: config, Tasks: tasks, Mailbox: mailbox, Now: func() time.Time { return time.UnixMilli(20000) }, HistoryBudget: sessionstore.NewBudget()})
	for i, row := range input {
		session := row.String("session")
		var failure error
		for _, snapshot := range rows(row.Get("snapshots")) {
			if failure = reader.ArchiveSnapshot(context.Background(), snapshot); failure != nil {
				break
			}
		}
		mark := obj(row.Get("mark"))
		if failure == nil {
			failure = reader.MarkArchiveDeleted(context.Background(), mark.String("team"), session, mark.String("incarnation"))
		}
		var errorText, physical any
		if failure != nil {
			errorText = failure.Error()
		}
		if data, e := os.ReadFile(reader.ArchivePath(session)); e == nil {
			physical = string(data)
		}
		actual := raw(map[string]any{"document": reader.ReadArchive(session), "selected": []any{}, "physical": physical, "error": errorText})
		var a, b any
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(source.Observations[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("case %d Go %s Source %s", i, actual, source.Observations[i])
		}
	}
}
