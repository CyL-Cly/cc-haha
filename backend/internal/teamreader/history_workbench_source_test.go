package teamreader

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

func TestActualLegacyWorkbenchProjectsCreationsResultsEdgesMessagesAndIncarnationsMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	tool := func(id, name string, input protocol.JSONObject, n int) protocol.JSONObject {
		return protocol.NewJSONObject(field("id", id), field("type", "tool_use"), field("timestamp", time.Unix(int64(n), 0).UTC().Format("2006-01-02T15:04:05.000Z")), field("cwd", "/actual/source"), field("content", []protocol.JSONObject{protocol.NewJSONObject(field("type", "tool_use"), field("id", id), field("name", name), field("input", input))}))
	}
	result := func(id string, value protocol.JSONObject, n int, isError bool) protocol.JSONObject {
		return protocol.NewJSONObject(field("id", id+"-result"), field("type", "tool_result"), field("timestamp", time.Unix(int64(n), 0).UTC().Format("2006-01-02T15:04:05.000Z")), field("content", []protocol.JSONObject{protocol.NewJSONObject(field("type", "tool_result"), field("tool_use_id", id), field("is_error", isError))}), field("toolUseResult", value))
	}
	create := tool("create", "TeamCreate", protocol.NewJSONObject(field("team_name", "requested"), field("description", "real team"), field("agent_type", "custom-leader")), 1)
	created := result("create", protocol.NewJSONObject(field("team_name", "actual-team"), field("lead_agent_id", "leader@actual-team")), 2, false)
	agent := tool("agent", "Agent", protocol.NewJSONObject(field("name", "worker"), field("subagent_type", "custom")), 3)
	spawned := result("agent", protocol.NewJSONObject(field("agent_id", "w@actual-team"), field("agent_type", "runtime-custom"), field("model", "actual-model"), field("color", "blue"), field("tmux_session_name", "in-process")), 4, false)
	first := tool("task1", "TaskCreate", protocol.NewJSONObject(field("subject", "input subject"), field("description", "description"), field("activeForm", "Working")), 5)
	firstResult := result("task1", protocol.NewJSONObject(field("task", protocol.NewJSONObject(field("id", "1"), field("subject", "result subject")))), 6, false)
	update := tool("update", "TaskUpdate", protocol.NewJSONObject(field("taskId", "1"), field("owner", "worker"), field("status", "in_progress"), field("addBlocks", []any{"2", "2", false, ""}), field("metadata", protocol.NewJSONObject(field("future", true)))), 7)
	next := tool("task2", "TaskCreate", protocol.NewJSONObject(field("subject", "Second")), 8)
	nextResult := result("task2", protocol.NewJSONObject(field("task", protocol.NewJSONObject(field("id", "2")))), 9, false)
	send := tool("send", "SendMessage", protocol.NewJSONObject(field("type", "broadcast"), field("content", "real message"), field("summary", "actual")), 10)
	user := protocol.NewJSONObject(field("id", "teammate"), field("type", "user"), field("timestamp", "1970-01-01T00:00:11.000Z"), field("content", `<teammate-message teammate_id="worker" color="blue"> Real worker reply </teammate-message>`))
	list := tool("list", "TaskList", protocol.NewJSONObject(), 12)
	listed := result("list", protocol.NewJSONObject(field("tasks", []protocol.JSONObject{protocol.NewJSONObject(field("id", "1"), field("status", "completed"), field("subject", "Final"), field("blockedBy", []string{})), protocol.NewJSONObject(field("id", "2"), field("status", "pending"), field("blockedBy", []string{"1"}))})), 13, false)
	deleteTool := tool("delete", "TeamDelete", protocol.NewJSONObject(), 14)
	deleted := result("delete", protocol.NewJSONObject(field("finalTasks", []protocol.JSONObject{protocol.NewJSONObject(field("id", "2"), field("subject", "only final"), field("status", "completed"), field("blocks", []string{}), field("blockedBy", []string{}))})), 15, false)
	base := []protocol.JSONObject{create, created, agent, spawned, first, firstResult, update, next, nextResult, send, user, list, listed, deleteTool, deleted}
	cases := [][]protocol.JSONObject{base, base[:2], base[:10], append(append([]protocol.JSONObject(nil), base...), tool("recreate", "TeamCreate", protocol.NewJSONObject(field("team_name", "actual-team")), 20)), {create, result("create", protocol.NewJSONObject(field("success", false)), 2, false)}, {create, result("create", protocol.NewJSONObject(), 2, true)}, append(append([]protocol.JSONObject(nil), base[:10]...), result("update", protocol.NewJSONObject(field("success", false)), 7, false)), append(append([]protocol.JSONObject(nil), base[:10]...), tool("remove", "TaskUpdate", protocol.NewJSONObject(field("taskId", "1"), field("status", "deleted")), 11)), {create, created, tool("explicit", "Agent", protocol.NewJSONObject(field("team_name", "other"), field("name", "worker")), 3)}, {}}
	// Reverse delivery order must still honor timestamps and stable equal-time order.
	reversed := append([]protocol.JSONObject(nil), base...)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	cases = append(cases, reversed)
	input := []protocol.JSONObject{}
	for _, messages := range cases {
		input = append(input, protocol.NewJSONObject(field("project", protocol.NewJSONObject(field("session", "actual-session"), field("messages", messages)))))
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-transcript-parser.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_TRANSCRIPT_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"homeDir": home, "rows": input}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Pages        []json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Pages) != len(cases) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(output))
	}
	tasks, _ := taskstore.New(taskstore.Options{ConfigDir: config, PID: os.Getpid(), Now: time.Now})
	mailbox, _ := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: config, TaskStore: tasks})
	reader, _ := New(Options{ConfigDir: config, Tasks: tasks, Mailbox: mailbox, Now: time.Now})
	for i, messages := range cases {
		actual := raw(reader.ProjectWorkbenches("actual-session", messages))
		var a, b any
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(source.Pages[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("case %d Go %s Source %s", i, actual, source.Pages[i])
		}
	}
}
