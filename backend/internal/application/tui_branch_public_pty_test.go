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

func TestActualPublicCLIBranchForkNextRequestParentImmutableAndExitPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix actual PTY; ConPTY separate")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "claude-haha-branch")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, goTool, "build", "-p", "2", "-tags", "native_process_fixture", "-o", binary, "../../cmd/claude-haha")
	build.Env = os.Environ()
	capturePublicGoCoverage(t, build)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("actual CLI build %v %.4000s", err, output)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "owned-branch-pty"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	childCtx, stop := context.WithTimeout(context.Background(), 35*time.Second)
	defer stop()
	command := exec.CommandContext(childCtx, python, "testdata/tui_branch_public_pty.py", binary, root)
	command.Env = []string{"HOME=" + root, "TMPDIR=" + root, "PATH=/usr/bin:/bin"}
	capturePublicGoCoverage(t, command)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("actual public branch/fork %v %.8000s", err, output)
	}
	for _, value := range []string{`"actualPublicCLI": true`, `"branchAndAlias": true`, `"parentsImmutable": true`, `"modelRequests": 3`, `"terminalRestored": true`, `"externalRequests": []`} {
		if !strings.Contains(string(output), value) {
			t.Fatal(value, string(output))
		}
	}
}
