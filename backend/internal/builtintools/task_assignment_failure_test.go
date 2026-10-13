package builtintools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

func TestCommittedTaskAssignmentMailboxFailureIsDiagnosticNotToolFailure(t *testing.T) {
	committedTaskAssignmentFailure(t)
}

func committedTaskAssignmentFailure(t *testing.T) protocol.JSONObject {
	t.Helper()
	s, owner, _, _ := taskFixture(t)
	if _, e := invokeTask(t, s, owner, "TaskCreate", input(`{"subject":"committed task","description":"actual blocked mailbox"}`)); e != nil {
		t.Fatal(e)
	}
	mailbox, e := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: filepath.Join(s.options.Home, ".claude"), TaskStore: s.options.TaskStore})
	if e != nil {
		t.Fatal(e)
	}
	blocker := filepath.Dir(mailbox.Path("worker", "tasks"))
	if e = os.MkdirAll(filepath.Dir(blocker), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(blocker, []byte("existing file blocks inbox creation"), 0600); e != nil {
		t.Fatal(e)
	}
	var logged []error
	s.options.Team = &TeamBindings{
		TeamName: func(*cooperative.Scope) (string, error) { return "tasks", nil },
		TaskAssigned: func(team string, task protocol.JSONObject) error {
			return mailbox.Write(task.String("owner"), team, input(`{"from":"team-lead","text":"actual assignment"}`))
		},
		TaskAssignmentError: func(scope *cooperative.Scope, err error) {
			if !scope.BelongsTo(owner) {
				t.Error("assignment diagnostic escaped task owner")
			}
			logged = append(logged, err)
		},
	}
	result, e := invokeTask(t, s, owner, "TaskUpdate", input(`{"taskId":"1","owner":"worker"}`))
	if e != nil || !boolean(input(string(result.Data)), "success", false) || len(logged) != 1 {
		t.Fatalf("committed result %s error=%v diagnostics=%v", result.Data, e, logged)
	}
	task, e := s.options.TaskStore.Get("tasks", quote("1"))
	if e != nil || task == nil || task.String("owner") != "worker" || task.String("status") != "pending" {
		t.Fatalf("committed task %v error=%v", task, e)
	}
	if info, err := os.Stat(blocker); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("mailbox blocker changed: %v %v", info, err)
	}
	if _, err := os.Stat(mailbox.Path("worker", "tasks")); err == nil {
		t.Fatal("failed notification produced an inbox")
	}
	// Reassigning the same owner must not replay an already committed assignment.
	if _, e = invokeTask(t, s, owner, "TaskUpdate", input(`{"taskId":"1","owner":"worker"}`)); e != nil || len(logged) != 1 {
		t.Fatalf("duplicate assignment error=%v diagnostics=%v", e, logged)
	}
	mapped, e := MapResult("TaskUpdate", result.Data, quote("actual-task-assignment"))
	if e != nil {
		t.Fatal(e)
	}
	return mapped
}

func TestActualSourceCommittedTaskRetainsSuccessAfterPhysicalMailboxFailure(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	if e := os.MkdirAll(filepath.Join(home, ".claude"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.MkdirAll(filepath.Join(home, "tmp"), 0700); e != nil {
		t.Fatal(e)
	}
	fixture, e := filepath.Abs("../../../scripts/quality-gate/go-parity/native-task-assignment-failure.fixture.ts")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	child.Dir = home
	child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TASK_ASSIGNMENT_FAILURE_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "TZ=UTC"}
	child.Stdin = bytes.NewReader(raw(map[string]string{"homeDir": home}))
	var stderr bytes.Buffer
	child.Stderr = &stderr
	data, e := child.Output()
	if e != nil {
		t.Fatalf("actual TaskUpdate Source %v %.4000s", e, stderr.String())
	}
	var source struct {
		Sent, BlockerIsFile, InboxExists bool
		Data, Mapped, Task               protocol.JSONObject
		DiagnosticErrors                 []string
		Capabilities                     struct{ Blocked []string }
	}
	if e = json.Unmarshal(data, &source); e != nil || len(source.Capabilities.Blocked) > 0 || source.Sent || !source.BlockerIsFile || source.InboxExists {
		t.Fatalf("Source physical receipt %s error=%v", data, e)
	}
	if !boolean(source.Data, "success", false) || source.Task.String("owner") != "worker" || source.Task.String("status") != "pending" || source.Mapped.String("content") != "Updated task #1 owner" {
		t.Fatalf("Source committed result %s", data)
	}
	if len(source.DiagnosticErrors) != 2 {
		t.Fatalf("actual failed probe and assignment must be diagnostic: %s", data)
	}
	// The paired native test above exercises the same real filesystem failure,
	// task transaction and owner diagnostic boundary, without a model or a stub.
	mapped := committedTaskAssignmentFailure(t)
	if !bytes.Equal(raw(mapped), raw(source.Mapped)) {
		t.Fatalf("committed native/Source result differs: %s vs %s", raw(mapped), raw(source.Mapped))
	}
}
