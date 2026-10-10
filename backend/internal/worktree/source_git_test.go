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

func TestGitWorktreeActualSourceCreationResumeSetupCommandsAndRemoval(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.MkdirAll(config, 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-worktree-git.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WORKTREE_GIT_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	type command struct {
		Args     []string
		Cwd      string
		NoPrompt bool
	}
	var source struct {
		Session, Resumed                                 Session
		CreationCommands, ResumeCommands, RemoveCommands []command
		Copied, Link, Home                               string
		Removed, Branch                                  bool
		Capabilities                                     struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &source); e != nil || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, string(out))
	}
	s, root, env := privateGit(t)
	_ = os.WriteFile(filepath.Join(root, ".gitignore"), []byte("node_modules\n.claude/settings.local.json\n"), 0644)
	_, _ = s.run(ctx, root, []string{"add", ".gitignore"}, false)
	_, _ = s.run(ctx, root, []string{"commit", "--amend", "--no-edit"}, false)
	_ = os.MkdirAll(filepath.Join(root, ".claude"), 0700)
	_ = os.WriteFile(filepath.Join(root, ".claude/settings.local.json"), []byte(`{"future":"keep","env":{"fake":"value"}}`), 0600)
	_ = os.MkdirAll(filepath.Join(root, "node_modules"), 0700)
	commands := []command{}
	s.options.Run = func(ctx context.Context, r ProcessRequest) (ProcessResult, error) {
		commands = append(commands, command{r.Arguments, r.CWD, r.Environment["GIT_TERMINAL_PROMPT"] == "0" && r.Environment["GIT_ASKPASS"] == "" && r.IgnoreStdin})
		return NativeRun(ctx, r)
	}
	request := Request{CWD: root, SessionID: "11111111-1111-4111-8111-111111111111", Slug: "user/feature", Settings: Settings{SymlinkDirectories: []string{"node_modules"}}}
	session, e := s.Create(ctx, request)
	if e != nil {
		t.Fatal(e)
	}
	creationCommands := commands
	commands = []command{}
	resumed, e := s.Create(ctx, request)
	if e != nil {
		t.Fatal(e)
	}
	resumeCommands := commands
	commands = []command{}
	copied, _ := os.ReadFile(filepath.Join(session.Path, ".claude/settings.local.json"))
	link, _ := os.Readlink(filepath.Join(session.Path, "node_modules"))
	_, e = s.Remove(ctx, session)
	if e != nil {
		t.Fatal(e)
	}
	_, statError := os.Stat(session.Path)
	branch, e := NativeRun(ctx, ProcessRequest{Command: s.options.GitExecutable, CWD: root, Environment: env, Arguments: []string{"show-ref", "--verify", "refs/heads/" + session.Branch}})
	if e != nil {
		t.Fatal(e)
	}
	normalize := func(v any) any {
		b, _ := json.Marshal(v)
		text := strings.ReplaceAll(string(b), source.Home, filepath.Dir(root))
		var normalized any
		_ = json.Unmarshal([]byte(text), &normalized)
		return normalized
	}
	for _, pair := range [][2]any{{normalize(source.Session), normalize(session)}, {normalize(source.Resumed), normalize(resumed)}, {normalize(source.CreationCommands), normalize(creationCommands)}, {normalize(source.ResumeCommands), normalize(resumeCommands)}, {normalize(source.RemoveCommands), normalize(commands)}} {
		if !reflect.DeepEqual(pair[0], pair[1]) {
			t.Errorf("Source actual git differences\nSource%+v\nGo%+v", pair[0], pair[1])
		}
	}
	if string(copied) != source.Copied || link != strings.ReplaceAll(source.Link, source.Home, filepath.Dir(root)) || !source.Removed || !source.Branch || !os.IsNotExist(statError) || branch.Code == 0 {
		t.Fatal("actual file/git effects differed", string(copied), link, statError, branch)
	}
}
