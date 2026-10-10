package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
)

func teamWorkerFixture(t *testing.T, start teamworker.Start, baseURL string) (*MainPrint, nativePluginFixture, *bytes.Buffer) {
	t.Helper()
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false},"agentTeamsEnabled":true,"effortLevel":"high","alwaysThinkingEnabled":false}`)
	f.n.Environment["ANTHROPIC_API_KEY"] = "private-team-fixture-key"
	f.n.Environment["ANTHROPIC_BASE_URL"] = baseURL
	f.n.Environment["CLAUDE_CODE_ENABLE_TASKS"] = "1"
	f.n.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = "1"
	f.n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
	f.n.Environment["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "true"
	f.n.Environment["CLAUDE_CODE_TASK_LIST_ID"] = "unrelated-standalone-list"
	f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
	f.n.Environment["DISABLE_TELEMETRY"] = "1"
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	f.n.Environment["ENABLE_TOOL_SEARCH"] = "false"
	f.n.Environment["PATH"] = "/usr/bin:/bin"
	f.write(t, filepath.Join(f.cwd, "CLAUDE.md"), "DO_NOT_SEND_WORKER_CLAUDE_MD")
	f.write(t, filepath.Join(f.cwd, ".claude", "skills", "prime", "SKILL.md"), "---\ndescription: Actual worker preload\nmodel: sonnet\neffort: low\n---\nWORKER_SKILL_CONTENT_ONCE")
	disabled := false
	stderr := &bytes.Buffer{}
	m, err := NewMainPrint(MainPrintOptions{TeamWorker: &start, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-team-model", SystemPrompt: "leader system must be replaced", Tools: []string{"Read"}, PermissionMode: "bypassPermissions", MaxTurns: 10, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: stderr})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	return m, f, stderr
}

func TestActualTeamWorkerMainTasksQuestionMailboxAndStandaloneTranscript(t *testing.T) {
	names := []string{"Read", "Write", "AskUserQuestion", "TaskCreate", "TaskGet", "TaskList", "TaskUpdate", "SendMessage"}
	definition := protocol.NewJSONObject(protocol.JSONField{Name: "agentType", Value: quoteMain("plugin:approved")}, protocol.JSONField{Name: "source", Value: quoteMain("plugin")}, protocol.JSONField{Name: "skills", Value: rawMain([]string{"prime"})}, protocol.JSONField{Name: "omitClaudeMd", Value: json.RawMessage("true")})
	start := teamworker.Start{ParentSessionID: "11111111-1111-4111-8111-111111111111", TeamName: "MyTeam_", MemberID: "member-1", Name: "worker", SystemPrompt: "ACTUAL_APPROVED_WORKER_SYSTEM", Tools: &names, AgentDefinition: definition}
	var mu sync.Mutex
	requests := [][]byte{}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, body)
		round := len(requests)
		mu.Unlock()
		switch round {
		case 1:
			emitWorktreeModel(w, round, "TaskCreate", `{"subject":"shared proof","description":"real shared task"}`, "")
		case 2:
			emitWorktreeModel(w, round, "TaskUpdate", `{"taskId":"1","status":"in_progress","owner":"worker"}`, "")
		case 3:
			emitWorktreeModel(w, round, "TaskGet", `{"taskId":"1"}`, "")
		case 4:
			emitWorktreeModel(w, round, "SendMessage", `{"to":"team-lead","message":"actual worker finished its shared task","summary":"proof"}`, "")
		case 5:
			emitWorktreeModel(w, round, "AskUserQuestion", `{"questions":[{"question":"Approve worker proof?","header":"Proof","options":[{"label":"Go","description":"Continue"},{"label":"Stop","description":"Stop"}],"multiSelect":false}]}`, "")
		case 6:
			emitWorktreeModel(w, round, "Write", `{"file_path":"worker-proof.txt","content":"Go"}`, "")
		case 7:
			emitWorktreeModel(w, round, "Write", `{"file_path":"worker-proof.txt","content":"Go checkpointed"}`, "")
		case 8, 9:
			emitWorktreeModel(w, round, "", "", "actual worker complete")
		default:
			t.Errorf("unexpected model round %d", round)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer model.Close()
	// The process primitive receives the actual loopback origin; no model
	// consumer, tool result or transcript writer is substituted.
	m, f, stderr := teamWorkerFixture(t, start, model.URL)
	permissions := 0
	m.options.ResolvePermission = func(_ context.Context, scope *cooperative.Scope, tool queryloop.RegisteredTool, in *protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
		if !scope.BelongsTo(m.Owner) || tool.Name != "AskUserQuestion" {
			t.Errorf("unexpected permission %s", tool.Name)
		}
		permissions++
		updated := in.Clone()
		updated.Set("answers", rawMain(map[string]string{"Approve worker proof?": "Go"}))
		return queryloop.ToolPermission{Behavior: "allow", Input: in, UpdatedInput: &updated}, nil
	}
	if err := m.StartSession(context.Background()); err != nil {
		t.Fatal(err, stderr.String())
	}
	if m.agentChild || m.options.DetachedAgent != nil {
		t.Fatal("worker was treated as ordinary sidechain")
	}
	if _, err := os.Stat(filepath.Join(f.config, "projects")); err == nil {
		t.Fatal("SDK initialization wrote a placeholder worker transcript")
	}
	for _, text := range []string{"perform approved task", "continue same worker"} {
		input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "uuid", Value: quoteMain(uuidMain())}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": text})})
		if err := m.RunTurn(context.Background(), input); err != nil {
			t.Fatal(err, stderr.String())
		}
	}
	var transcriptPath string
	if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		list, err := m.Controller.TaskListIDOnOwner(scope, "worker@MyTeam_")
		if err != nil || list != "myteam-" {
			t.Errorf("team list %q %v", list, err)
		}
		if _, err := m.Controller.TaskListIDOnOwner(scope, "foreign"); err == nil {
			t.Error("foreign task identity admitted")
		}
		transcriptPath, err = m.Controller.Writer().ComputedTranscriptPath(scope)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.ShutdownSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 9 || permissions != 1 {
		t.Fatalf("requests %d permissions %d errors %s", len(requests), permissions, stderr.String())
	}
	for _, body := range requests {
		var value protocol.JSONObject
		_ = value.UnmarshalJSON(body)
		if value.String("model") != "fixture-team-model" || bytes.Contains(body, []byte("DO_NOT_SEND_WORKER_CLAUDE_MD")) || bytes.Contains(body, []byte("leader system must be replaced")) || !bytes.Contains(body, []byte("ACTUAL_APPROVED_WORKER_SYSTEM")) {
			t.Fatalf("human worker route/prompt %s", body)
		}
		if bytes.Count(body, []byte("WORKER_SKILL_CONTENT_ONCE")) != 1 {
			t.Fatalf("preload not once %s", body)
		}
	}
	if !bytes.Contains(requests[5], []byte("Go")) || !bytes.Contains(requests[3], []byte("real shared task")) {
		t.Fatal("tool results absent from next physical request")
	}
	content, err := os.ReadFile(filepath.Join(f.cwd, "worker-proof.txt"))
	if err != nil || string(content) != "Go checkpointed" {
		t.Fatal(string(content), err)
	}
	data, err := os.ReadFile(transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"type":"file-history-snapshot"`)) || !bytes.Contains(data, []byte(`"backupFileName":null`)) {
		t.Fatalf("worker missing before creation checkpoint %s", data)
	}
	backupFiles, err := os.ReadDir(filepath.Join(f.config, "file-history", m.Controller.SessionID()))
	if err != nil || len(backupFiles) == 0 {
		t.Fatal("worker after checkpoint physical backup", err)
	}
	foundAfter := false
	for _, entry := range backupFiles {
		backup, _ := os.ReadFile(filepath.Join(f.config, "file-history", m.Controller.SessionID(), entry.Name()))
		if string(backup) == "Go checkpointed" {
			foundAfter = true
		}
	}
	if !foundAfter {
		t.Fatal("worker final physical checkpoint missing")
	}
	if strings.Contains(transcriptPath, "subagents") {
		t.Fatal("worker sidechain path", transcriptPath)
	}
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		var row protocol.JSONObject
		_ = row.UnmarshalJSON(line)
		if row.String("type") != "user" && row.String("type") != "assistant" {
			continue
		}
		if string(row.Get("isSidechain")) != "false" || row.String("teamName") != "MyTeam_" || row.String("agentName") != "worker" || row.Get("agentId") != nil {
			t.Fatalf("standalone main record %s", line)
		}
	}
	if _, err := os.Stat(filepath.Join(f.config, "tasks", "unrelated-standalone-list")); !os.IsNotExist(err) {
		t.Fatal("standalone list modified", err)
	}
	mailbox, err := os.ReadFile(filepath.Join(f.config, "teams", "MyTeam_", "inboxes", "team-lead.json"))
	if err != nil || !bytes.Contains(mailbox, []byte("actual worker finished")) {
		t.Fatal(string(mailbox), err)
	}
}
