package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNativeMCPOrdinaryWorktreePhysicalKeepRemoveCloseRestartActualSource(t *testing.T) {
	home, env := ordinaryFixture(t)
	cwd := filepath.Join(home, "repo")
	if e := os.Mkdir(cwd, 0700); e != nil {
		t.Fatal(e)
	}
	write := func(path, body string) {
		t.Helper()
		if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(path, []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
	}
	write(filepath.Join(cwd, "tracked.txt"), "owned tracked\n")
	write(filepath.Join(cwd, ".gitignore"), "node_modules\nprivate/\n.claude/settings.local.json\n")
	write(filepath.Join(cwd, ".worktreeinclude"), "private/*\n!private/excluded.txt\n")
	write(filepath.Join(cwd, "private/included.txt"), "actual include")
	write(filepath.Join(cwd, "private/excluded.txt"), "excluded")
	write(filepath.Join(cwd, ".claude/settings.local.json"), `{"worktree":{"symlinkDirectories":["node_modules"]},"future":{"keep":true}}`)
	if e := os.Mkdir(filepath.Join(cwd, "node_modules"), 0700); e != nil {
		t.Fatal(e)
	}
	git := func(args ...string) {
		t.Helper()
		out, e := worktree.NativeRun(context.Background(), worktree.ProcessRequest{Command: "/usr/bin/git", Arguments: args, CWD: cwd, Environment: env, IgnoreStdin: true})
		if e != nil || out.Code != 0 {
			t.Fatal("fixture git", args, e, out.Stderr)
		}
	}
	git("init", "-b", "main")
	git("add", ".")
	git("commit", "-m", "owned fixture")
	type call struct {
		name string
		args map[string]any
	}
	calls := []call{{"EnterWorktree", map[string]any{"name": "physical"}}, {"EnterWorktree", map[string]any{"name": "other"}}, {"Write", map[string]any{"file_path": "proof.txt", "content": "actual ordinary worktree"}}, {"ExitWorktree", map[string]any{"action": "remove"}}, {"ExitWorktree", map[string]any{"action": "keep"}}, {"Bash", map[string]any{"command": "pwd", "description": "actual cwd restored"}}, {"EnterWorktree", map[string]any{"name": "physical"}}, {"ExitWorktree", map[string]any{"action": "remove", "discard_changes": true}}, {"EnterWorktree", map[string]any{"name": "left-at-close"}}}
	source := startOrdinarySource(t, home, cwd, env)
	want := make([]protocol.JSONObject, len(calls))
	path := worktree.PathFor(cwd, "physical")
	assertSetup := func() {
		t.Helper()
		if b, e := os.ReadFile(filepath.Join(path, "private/included.txt")); e != nil || string(b) != "actual include" {
			t.Error("actual .worktreeinclude producer", e, string(b))
		}
		if _, e := os.Stat(filepath.Join(path, "private/excluded.txt")); !os.IsNotExist(e) {
			t.Error("include negation", e)
		}
		if b, e := os.ReadFile(filepath.Join(path, ".claude/settings.local.json")); e != nil || !strings.Contains(string(b), "future") {
			t.Error("actual local settings copy", e)
		}
		if target, e := os.Readlink(filepath.Join(path, "node_modules")); e != nil || target != filepath.Join(cwd, "node_modules") {
			t.Error("actual settings symlink", e, target)
		}
	}
	for i, c := range calls {
		want[i] = source.call(t, c.name, c.args)
		if i == 0 {
			assertSetup()
		}
	}
	source.disposeWatcher(t)
	left := worktree.PathFor(cwd, "left-at-close")
	if _, e := os.Stat(left); e != nil {
		t.Fatal("Source EOF lost owned worktree", e)
	}
	restart := startOrdinarySource(t, home, cwd, env)
	restartWant := restart.call(t, "ExitWorktree", map[string]any{"action": "remove", "discard_changes": true})
	restart.disposeWatcher(t)
	if _, e := os.Stat(left); e != nil {
		t.Fatal("Source restart affected old worktree", e)
	}
	git("worktree", "remove", "--force", left)
	git("branch", "-D", worktree.BranchName("left-at-close"))
	host, _ := ordinaryGoContext(t, home, cwd, env)
	for i, c := range calls {
		got, e := host.Call(context.Background(), c.name, rawMain(c.args))
		if e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(mcpOrdinaryResult(t, got), mcpOrdinaryResult(t, want[i])) {
			t.Errorf("%d %s Go=%v Source=%v", i, c.name, mcpOrdinaryResult(t, got), mcpOrdinaryResult(t, want[i]))
		}
		if i == 0 {
			assertSetup()
		}
	}
	if e := host.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(left); e != nil {
		t.Fatal("Go close lost worktree", e)
	}
	resumed, _ := ordinaryGoContext(t, home, cwd, env)
	got, e := resumed.Call(context.Background(), "ExitWorktree", rawMain(map[string]any{"action": "remove", "discard_changes": true}))
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(mcpOrdinaryResult(t, got), mcpOrdinaryResult(t, restartWant)) {
		t.Fatal("restart restored active worktree contrary to Source")
	}
	if _, e := os.Stat(left); e != nil {
		t.Fatal("Go restart affected old worktree", e)
	}
	git("worktree", "remove", "--force", left)
	git("branch", "-D", worktree.BranchName("left-at-close"))
}
