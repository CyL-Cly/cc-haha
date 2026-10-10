package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

func repositoryLaunchFixture(t *testing.T) (*Server, Options) {
	t.Helper()
	o := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	o.WorkDir = filepath.Join(o.HomeDir, "repo")
	if e := os.MkdirAll(o.WorkDir, 0700); e != nil {
		t.Fatal(e)
	}
	env := map[string]string{"HOME": o.HomeDir, "USERPROFILE": o.HomeDir, "PATH": "/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": filepath.Join(o.HomeDir, "absent-gitconfig"), "GIT_AUTHOR_NAME": "fixture", "GIT_AUTHOR_EMAIL": "fixture@example.invalid", "GIT_COMMITTER_NAME": "fixture", "GIT_COMMITTER_EMAIL": "fixture@example.invalid", "GIT_AUTHOR_DATE": "2026-10-01T00:00:00Z", "GIT_COMMITTER_DATE": "2026-10-01T00:00:00Z", "GIT_TERMINAL_PROMPT": "0", "GIT_ASKPASS": ""}
	o.Environment = func() map[string]string { return env }
	for _, args := range [][]string{{"init", "-b", "main"}} {
		r, e := worktree.NativeRun(context.Background(), worktree.ProcessRequest{Command: "/usr/bin/git", Arguments: args, CWD: o.WorkDir, Environment: env})
		if e != nil || r.Code != 0 {
			t.Fatal(r, e)
		}
	}
	_ = os.WriteFile(filepath.Join(o.WorkDir, "tracked.txt"), []byte("private repository"), 0644)
	for _, args := range [][]string{{"add", "tracked.txt"}, {"commit", "-m", "initial private repository"}, {"branch", "feature"}, {"update-ref", "refs/remotes/origin/remote-only", "HEAD"}} {
		r, e := worktree.NativeRun(context.Background(), worktree.ProcessRequest{Command: "/usr/bin/git", Arguments: args, CWD: o.WorkDir, Environment: env})
		if e != nil || r.Code != 0 {
			t.Fatal(r, e)
		}
	}
	s, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s, o
}
func observeRepository(value any, e error) map[string]any {
	if e == nil {
		return map[string]any{"value": value}
	}
	var api *apiError
	if errors.As(e, &api) {
		return map[string]any{"error": map[string]any{"code": api.code, "message": api.message, "status": api.status}}
	}
	return map[string]any{"error": map[string]any{"message": e.Error()}}
}
func TestRepositoryLaunchPlanningBranchMutationMatchesActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	home := t.TempDir()
	home, _ = filepath.EvalSymlinks(home)
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-repository-launch.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_REPOSITORY_LAUNCH_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var source struct {
		Rows, BranchRows []json.RawMessage
		Context          json.RawMessage
		Switched         json.RawMessage
		ActualBranch     string
		NotCreated       bool
		Home             string
		Capabilities     struct{ Blocked []string }
	}
	if e = json.Unmarshal(output, &source); e != nil || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(e, string(output))
	}
	s, o := repositoryLaunchFixture(t)
	normalize := func(data []byte) string {
		text := strings.ReplaceAll(string(data), filepath.Join(source.Home, "repo"), o.WorkDir)
		text = strings.ReplaceAll(text, source.Home, o.HomeDir)
		return string(protocolCanonicalJSONForRepository([]byte(text)))
	}
	options := []*sessionRepositoryOptions{nil, {}, {Branch: "feature"}, {Branch: "feature", Worktree: true}, {Worktree: true}, {Branch: "remote-only", Worktree: true}, {Branch: "missing"}, {Branch: "   "}, {Branch: "feature"}}
	for i, opt := range options {
		value, e := s.resolveSessionRepository(ctx, o.WorkDir, opt, "11111111-1111-4111-8111-111111111111")
		var actual any
		if e == nil {
			actual = map[string]any{"workDir": value.WorkDir}
			if value.Repository != nil {
				actual.(map[string]any)["repository"] = value.Repository
			}
		}
		b, _ := json.Marshal(observeRepository(actual, e))
		if normalize(source.Rows[i]) != normalize(b) {
			t.Errorf("plan%d Source%s Go%s", i, normalize(source.Rows[i]), normalize(b))
		}
	}
	branches := []struct{ name, from string }{{"topic", "remote-only"}, {"topic", ""}, {"worktree-desktop-reserved", ""}, {"-flag", ""}, {"bad\nname", ""}, {strings.Repeat("a", 201), ""}, {"", "missing"}, {"new", "missing"}}
	for i, branch := range branches {
		value, e := s.createRepositoryBranch(ctx, o.WorkDir, branch.name, branch.from)
		b, _ := json.Marshal(observeRepository(value, e))
		if normalize(source.BranchRows[i]) != normalize(b) {
			t.Errorf("branch%d Source%s Go%s", i, normalize(source.BranchRows[i]), normalize(b))
		}
	}
	actual, _ := json.Marshal(s.inspectRepository(ctx, o.WorkDir))
	if normalize(source.Context) != normalize(actual) {
		t.Errorf("context Source%s Go%s", normalize(source.Context), normalize(actual))
	}
	switched, e := s.switchRepositoryCheckout(ctx, o.WorkDir, sessionRepository{Branch: "feature"})
	var value any
	if e == nil {
		value = map[string]any{"workDir": switched.WorkDir, "repository": switched.Repository}
	}
	actual, _ = json.Marshal(observeRepository(value, e))
	if normalize(source.Switched) != normalize(actual) {
		t.Errorf("switch Source%s Go%s", normalize(source.Switched), normalize(actual))
	}
	branch := s.repositoryGit(ctx, o.WorkDir, "branch", "--show-current")
	if trimJS(branch.stdout) != source.ActualBranch || !source.NotCreated {
		t.Fatal("actual branch/plan effects", branch, source.NotCreated)
	}
}
func protocolCanonicalJSONForRepository(raw []byte) []byte {
	var data any
	if json.Unmarshal(raw, &data) != nil {
		return raw
	}
	b, _ := json.Marshal(data)
	return b
}
