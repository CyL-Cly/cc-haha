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
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

func TestActualDeleteTeamRefusesLiveRosterCapturesTerminalAndRemovesPhysicalDirectoriesMatchSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	files := []map[string]string{}
	input := []protocol.JSONObject{}
	for i, name := range []string{"active", "remaining", "idle", "absent"} {
		session := "aaaaaaaa-aaaa-4aaa-8aaa-00000000000" + string(rune('0'+i))
		members := []protocol.JSONObject{protocol.NewJSONObject(field("agentId", "lead@"+name), field("name", "team-lead"), field("isActive", i == 0), field("cwd", "/actual/source"), field("tmuxPaneId", ""))}
		if i == 1 {
			members = append(members, protocol.NewJSONObject(field("agentId", "worker@remaining"), field("name", "worker"), field("isActive", false)))
		}
		config := protocol.NewJSONObject(field("name", name), field("createdAt", 1000), field("leadSessionId", session), field("leadAgentId", "lead@"+name), field("members", members))
		if i < 3 {
			files = append(files, map[string]string{"path": ".claude/teams/" + name + "/config.json", "content": string(raw(config))}, map[string]string{"path": ".claude/tasks/" + name + "/1.json", "content": `{"id":"1","subject":"Terminal source task","description":"","status":"completed","blocks":[],"blockedBy":[]}`})
		}
		input = append(input, protocol.NewJSONObject(field("session", session), field("delete", name)))
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
	config, _ := teamstore.NewConfig(teamstore.ConfigOptions{ConfigDir: directory, TaskStore: tasks, PID: os.Getpid()})
	env := map[string]string{"HOME": root, "PATH": "/usr/bin:/bin", "CLAUDE_CONFIG_DIR": directory}
	worktrees, err := worktree.New(worktree.Options{Files: worktree.OSFiles{}, Run: worktree.NativeRun, GitExecutable: worktree.NativeGitExecutable(env), Environment: env, Now: now, Log: func(string, string) {}})
	if err != nil {
		t.Fatal(err)
	}
	reader, _ := New(Options{ConfigDir: directory, Tasks: tasks, Mailbox: mailbox, Now: now, HistoryBudget: sessionstore.NewBudget(), CleanupTeam: func(ctx context.Context, name string, scoped *taskstore.Service) error {
		lifecycle, err := teamstore.NewLifecycle(teamstore.LifecycleOptions{Config: config, Tasks: scoped, RuntimeResources: func(ctx context.Context, _ string, members []protocol.JSONObject, _ bool) error {
			for _, member := range members {
				if path := member.String("worktreePath"); path != "" {
					if err := worktrees.DestroyTeamWorktree(ctx, path, os.RemoveAll); err != nil {
						return err
					}
				}
			}
			return nil
		}})
		if err != nil {
			return err
		}
		_, err = lifecycle.Cleanup(ctx, name, nil, false)
		return err
	}})
	for i, row := range input {
		name := row.String("delete")
		err := reader.DeleteTeam(context.Background(), name)
		var failure any
		if err != nil {
			failure = err.Error()
		}
		_, teamError := os.Stat(filepath.Join(directory, "teams", name))
		_, tasksError := os.Stat(filepath.Join(directory, "tasks", name))
		actual := raw(map[string]any{"document": reader.ReadArchive(row.String("session")), "error": failure, "teamExists": teamError == nil, "tasksExist": tasksError == nil})
		var a, b any
		_ = json.Unmarshal(actual, &a)
		_ = json.Unmarshal(source.Observations[i], &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("case %d Go %s Source %s", i, actual, source.Observations[i])
		}
	}
}
