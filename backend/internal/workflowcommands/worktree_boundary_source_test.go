package workflowcommands

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

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
)

func TestActualSourceWorkflowProjectWalkValidatesWorktreeStructureAndBacklink(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	h := newHost(t)
	write := func(name, value string) {
		path := filepath.Join(h.home, name)
		if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(path, []byte(value), 0600); e != nil {
			t.Fatal(e)
		}
	}
	dir := func(name string) {
		if e := os.MkdirAll(filepath.Join(h.home, name), 0700); e != nil {
			t.Fatal(e)
		}
	}
	dir("repo/.claude/workflows")
	rows := []struct{ CWD, ProjectRoot string }{
		{"repo/.claude/worktrees/valid", "repo"},
		{"repo/.claude/worktrees/alias", "repo"},
		{"repo/vendor/submodule", "repo"},
		{"repo/vendor/forged-layout", "repo"},
		{"repo/.claude/worktrees/forged-backlink", "repo"},
	}
	for _, row := range rows {
		if row.CWD != "repo/.claude/worktrees/alias" {
			dir(row.CWD + "/.claude/workflows")
		}
	}
	write("repo/.claude/worktrees/valid/.git", "gitdir: "+filepath.Join(h.home, "repo/.git/worktrees/valid"))
	write("repo/.git/worktrees/valid/commondir", "../..")
	write("repo/.git/worktrees/valid/gitdir", filepath.Join(h.home, "repo/.claude/worktrees/valid/.git"))
	if e := os.Symlink(filepath.Join(h.home, "repo/.claude/worktrees/valid"), filepath.Join(h.home, "repo/.claude/worktrees/alias")); e != nil {
		t.Fatal(e)
	}
	write("repo/vendor/submodule/.git", "gitdir: "+filepath.Join(h.home, "repo/.git/modules/submodule"))
	dir("repo/.git/modules/submodule")
	write("repo/vendor/forged-layout/.git", "gitdir: "+filepath.Join(h.home, "attacker/internal"))
	write("attacker/internal/commondir", filepath.Join(h.home, "repo/.git"))
	write("attacker/internal/gitdir", filepath.Join(h.home, "repo/vendor/forged-layout/.git"))
	write("repo/.claude/worktrees/forged-backlink/.git", "gitdir: "+filepath.Join(h.home, "repo/.git/worktrees/forged-backlink"))
	write("repo/.git/worktrees/forged-backlink/commondir", "../..")
	write("repo/.git/worktrees/forged-backlink/gitdir", filepath.Join(h.home, "repo/.claude/worktrees/valid/.git"))
	dir("tmp")
	inputRows := []map[string]string{}
	for _, row := range rows {
		inputRows = append(inputRows, map[string]string{"cwd": row.CWD, "projectRoot": row.ProjectRoot})
	}
	input, _ := json.Marshal(map[string]any{"homeDir": h.home, "projectBoundaries": inputRows})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-workflow.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = h.home
	command.Stdin = bytes.NewReader(input)
	command.Env = []string{"HOME=" + h.home, "USERPROFILE=" + h.home, "CLAUDE_CONFIG_DIR=" + h.config, "TMPDIR=" + filepath.Join(h.home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(h.home), "CC_HAHA_NATIVE_WORKFLOW_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if e := command.Run(); e != nil {
		t.Fatalf("Source boundary %v %.1600s %.1600s", e, stdout.String(), stderr.String())
	}
	var source struct {
		ProjectBoundaries [][]string
		Capabilities      struct{ Blocked []string }
	}
	if e := json.Unmarshal(stdout.Bytes(), &source); e != nil || len(source.ProjectBoundaries) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, stdout.String(), stderr.String())
	}
	h.service.options.CanonicalGitRoot = func(scope *cooperative.Scope, start string) (string, error) {
		if !scope.BelongsTo(h.owner) {
			return "", cooperative.ErrRestricted
		}
		return usercontext.CanonicalGitRoot(usercontext.OSMemoryFiles{}, filepath.EvalSymlinks, start), nil
	}
	for i, row := range rows {
		h.run(t, func(scope *cooperative.Scope) error {
			if e := h.state.SetProjectRoot(scope, filepath.Join(h.home, row.ProjectRoot)); e != nil {
				return e
			}
			dirs, e := h.service.projectDirectories(scope, filepath.Join(h.home, row.CWD))
			if e != nil {
				return e
			}
			if !reflect.DeepEqual(dirs, source.ProjectBoundaries[i]) {
				t.Errorf("Source worktree boundary %s differs Go%v Source%v", row.CWD, dirs, source.ProjectBoundaries[i])
			}
			return nil
		})
	}
	// The optional primitive is proof-bound by the native caller. An unbound
	// regular .git file keeps the migration guard rather than guessing trust.
	h.service.options.CanonicalGitRoot = nil
	h.run(t, func(scope *cooperative.Scope) error {
		_, e := h.service.projectDirectories(scope, filepath.Join(h.home, rows[0].CWD))
		if e == nil {
			t.Error("unbound canonical security guard removed")
		}
		return nil
	})
}
