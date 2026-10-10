package main

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestActualOriginalTmuxEntrypointBootstrapAndRawFlagGate(t *testing.T) {
	environments := []map[string]string{}
	for _, remote := range []string{"", "true", "True", "1", "false"} {
		for _, node := range []string{"", "--inspect", "--max-old-space-size=4096"} {
			environments = append(environments, map[string]string{"COREPACK_ENABLE_AUTO_PIN": "1", "CLAUDE_CODE_REMOTE": remote, "NODE_OPTIONS": node, "PRIVATE_FUTURE": "kept"})
		}
	}
	args := [][]string{{}, {"--tmux"}, {"--worktree"}, {"--tmux", "-w"}, {"--tmux=classic", "--worktree=name", "--bare"}, {"--tmux=other", "-w", "name"}, {"--tmux=", "--worktree=name"}, {"--tmux", "--worktree=", "--unknown"}, {"--", "--tmux", "--worktree=after"}, {"--tmux", "--worktree-name"}, {"--tmux", "--worktree=name"}, {"-w", "x", "--tmux=classic"}}
	home := t.TempDir()
	fixture, _ := filepath.Abs("testdata/tmux-entry.fixture.ts")
	input, _ := json.Marshal(map[string]any{"home": home, "environments": environments, "args": args})
	cmd := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var source struct {
		Environments []map[string]string
		Gates        []bool
	}
	if e = json.Unmarshal(out, &source); e != nil {
		t.Fatal(e, string(out))
	}
	for i, env := range environments {
		before, _ := json.Marshal(env)
		actual := tmuxEntrypointEnvironment(env)
		after, _ := json.Marshal(env)
		if !bytes.Equal(before, after) {
			t.Fatal("source observation mutated caller map")
		}
		if !reflect.DeepEqual(actual, source.Environments[i]) {
			t.Fatal(actual, source.Environments[i])
		}
	}
	for i, args := range args {
		if actual := worktree.HasTmuxWorktreeFastPath(args); actual != source.Gates[i] {
			t.Fatal(args, actual, source.Gates[i])
		}
	}
}
