package worktree

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAgentWorktreeActualSourceGitAndLifecycle(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.MkdirAll(config, 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-agent-worktree.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_AGENT_WORKTREE_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	type command struct {
		Args     []string
		Cwd      string
		NoPrompt bool
	}
	var source struct {
		Attempt, Resumed, Unavailable                                               AgentAttempt
		Touched, Clean, Dirty, Committed, Removed, RemovedPhysically, BranchRemoved bool
		CreationCommands, ResumeCommands, ChangesCommands, RemoveCommands           []command
		Home                                                                        string
		Current                                                                     json.RawMessage
		Capabilities                                                                struct{ Blocked []string }
	}
	if e = json.Unmarshal(output, &source); e != nil || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, string(output))
	}
	s, root, _ := privateGit(t)
	s.options.TouchAgentWorktree = func(p string, now time.Time) error { return os.Chtimes(p, now, now) }
	commands := []command{}
	s.options.Run = func(ctx context.Context, r ProcessRequest) (ProcessResult, error) {
		commands = append(commands, command{r.Arguments, r.CWD, r.IgnoreStdin && r.Environment["GIT_TERMINAL_PROMPT"] == "0" && r.Environment["GIT_ASKPASS"] == ""})
		return NativeRun(ctx, r)
	}
	attempt, e := s.CreateAgent(ctx, root, "agent-a1234567", Settings{})
	if e != nil {
		t.Fatal(e)
	}
	creationCommands := commands
	commands = []command{}
	info := *attempt.Worktree
	_ = os.Chtimes(info.Path, time.Unix(0, 0), time.Unix(0, 0))
	resumed, e := s.CreateAgent(ctx, root, "agent-a1234567", Settings{})
	if e != nil {
		t.Fatal(e)
	}
	resumeCommands := commands
	commands = []command{}
	st, _ := os.Stat(info.Path)
	touched := st.ModTime().UnixMilli() > 0
	clean, e := s.HasAgentChanges(ctx, info)
	if e != nil {
		t.Fatal(e)
	}
	_ = os.WriteFile(filepath.Join(info.Path, "output.txt"), []byte("agent output"), 0600)
	dirty, e := s.HasAgentChanges(ctx, info)
	if e != nil {
		t.Fatal(e)
	}
	_ = os.Remove(filepath.Join(info.Path, "output.txt"))
	_ = os.WriteFile(filepath.Join(info.Path, "tracked.txt"), []byte("agent commit"), 0600)
	for _, args := range [][]string{{"add", "tracked.txt"}, {"commit", "-m", "agent commit"}} {
		r, e := NativeRun(ctx, ProcessRequest{Command: s.options.GitExecutable, CWD: info.Path, Arguments: args, Environment: s.options.Environment})
		if e != nil || r.Code != 0 {
			t.Fatal(r, e)
		}
	}
	committed, e := s.HasAgentChanges(ctx, info)
	if e != nil {
		t.Fatal(e)
	}
	changesCommands := commands
	commands = []command{}
	removed, e := s.RemoveAgent(ctx, info)
	if e != nil {
		t.Fatal(e)
	}
	removeCommands := commands
	commands = []command{}
	unavailable, e := s.CreateAgent(ctx, t.TempDir(), "invalid/../slug", Settings{})
	if e != nil {
		t.Fatal(e)
	}
	normalize := func(v any) any {
		b, _ := json.Marshal(v)
		var out any
		_ = json.Unmarshal([]byte(strings.ReplaceAll(string(b), source.Home, filepath.Dir(root))), &out)
		return out
	}
	for _, p := range [][2]any{{source.Attempt, attempt}, {source.Resumed, resumed}, {source.Unavailable, unavailable}, {source.CreationCommands, creationCommands}, {source.ResumeCommands, resumeCommands}, {source.ChangesCommands, changesCommands}, {source.RemoveCommands, removeCommands}} {
		if !reflect.DeepEqual(normalize(p[0]), normalize(p[1])) {
			t.Errorf("actual Source divergence\nSource%+v\nGo%+v", normalize(p[0]), normalize(p[1]))
		}
	}
	if touched != source.Touched || clean != source.Clean || dirty != source.Dirty || committed != source.Committed || removed != source.Removed || !source.RemovedPhysically || !source.BranchRemoved || string(source.Current) != "null" {
		t.Fatal("actual Source lifecycle effects", source, touched, clean, dirty, committed, removed)
	}
}
