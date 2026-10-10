package clibootstrap

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualSecurityReviewRunsRealGitPromptCommandsThenReadAndModelReport(t *testing.T) {
	f := worktreeMainFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"agentTeamsEnabled":false,"alwaysThinkingEnabled":false,"enabledPlugins":{"sample@mkt":false}}`)
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	// Physical isolated repository, including the upstream symbolic HEAD that
	// the original four prompt commands require. No network or saved identity.
	_ = os.RemoveAll(filepath.Join(f.cwd, ".git"))
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = f.cwd
		cmd.Env = []string{"HOME=" + f.home, "PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid"}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(args, err, string(out))
		}
	}
	runGit("init", "--quiet", "--initial-branch=main")
	f.write(t, filepath.Join(f.cwd, "app.py"), "import subprocess\ndef run(value):\n    return subprocess.run(['printf', value], shell=False)\n")
	runGit("add", "app.py")
	runGit("commit", "--quiet", "-m", "private base")
	runGit("update-ref", "refs/remotes/origin/main", "HEAD")
	runGit("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	f.write(t, filepath.Join(f.cwd, "app.py"), "import subprocess\n# PRIVATE_SECURITY_ACTUAL_DIFF_873\ndef run(value):\n    return subprocess.run(value, shell=True)\n")
	runGit("add", "app.py")
	runGit("commit", "--quiet", "-m", "private vulnerable change")
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := int(calls.Add(1))
		if n == 1 {
			if !bytes.Contains(body, []byte("PRIVATE_SECURITY_ACTUAL_DIFF_873")) || bytes.Contains(body, []byte("!`git status`")) || !bytes.Contains(body, []byte("FALSE POSITIVE FILTERING")) {
				t.Errorf("original actual git review prompt missing %.2800s", body)
			}
			emitWorktreeModel(w, n, "Read", `{"file_path":"app.py"}`, "")
		} else if n == 2 {
			if !bytes.Contains(body, []byte("shell=True")) {
				t.Error("actual code not read")
			}
			emitWorktreeModel(w, n, "", "", "# Command injection: app.py:4\nPRIVATE_SECURITY_SHARED_HARNESS_DONE_873")
		} else {
			t.Error("extra model calls", n)
		}
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	disabled := false
	var diagnostics bytes.Buffer
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual security prompt shared Harness", Tools: []string{"Read,Bash"}, PermissionMode: "default", MaxTurns: 4, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: &diagnostics})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": "/security-review FRONTEND_SECURITY_IGNORED_ARGS"})})
	if err = m.RunTurn(ctx, user); err != nil {
		t.Fatal(err, diagnostics.String())
	}
	if calls.Load() != 2 {
		t.Fatal("not actual Read→model security report", calls.Load())
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(f.config, "projects", "*", "*.jsonl"))
	if len(files) != 1 {
		t.Fatal(files)
	}
	disk, err := os.ReadFile(files[0])
	if err != nil || !bytes.Contains(disk, []byte("PRIVATE_SECURITY_SHARED_HARNESS_DONE_873")) || !bytes.Contains(disk, []byte("<command-name>/security-review")) || !bytes.Contains(disk, []byte("PRIVATE_SECURITY_ACTUAL_DIFF_873")) {
		t.Fatal("real prompt/report canonical missing", err)
	}
}
