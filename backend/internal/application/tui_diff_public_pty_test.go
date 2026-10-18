package application

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestActualPublicCLIDiffGitDetailLoadingEscAndGlobalCloseJoinNextHumanPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix actual PTY; ConPTY separate")
	}
	goTool, e := exec.LookPath("go")
	if e != nil {
		t.Fatal(e)
	}
	python, e := exec.LookPath("python3")
	if e != nil {
		t.Fatal(e)
	}
	binary := filepath.Join(t.TempDir(), "claude-haha-diff")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, goTool, "build", "-p", "2", "-tags", "native_process_fixture", "-o", binary, "../../cmd/claude-haha")
	build.Env = os.Environ()
	capturePublicGoCoverage(t, build)
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatalf("actual CLI build %v %.4000s", e, out)
	}
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, "owned-diff-pty"), []byte("private"), 0600); e != nil {
		t.Fatal(e)
	}
	childCtx, stop := context.WithTimeout(context.Background(), 35*time.Second)
	defer stop()
	cmd := exec.CommandContext(childCtx, python, "testdata/tui_diff_public_pty.py", binary, root)
	cmd.Env = []string{"HOME=" + root, "TMPDIR=" + root, "PATH=/usr/bin:/bin"}
	capturePublicGoCoverage(t, cmd)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("actual public /diff %v %.7500s", e, out)
	}
	for _, value := range []string{`"actualPublicCLI": true`, `"escCanceledJoined": true`, `"globalCloseJoined": true`, `"nextHumanRequests": 1`, `"terminalRestored": true`, `"externalRequests": []`} {
		if !strings.Contains(string(out), value) {
			t.Fatal(value, string(out))
		}
	}
}
