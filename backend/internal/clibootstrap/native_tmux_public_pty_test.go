package clibootstrap

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

func TestActualPublicGoTmuxWorktreeClassicControlExistingPTYAndCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("actual Unix tmux PTY")
	}
	tmux, e := exec.LookPath("tmux")
	if e != nil {
		t.Skip("actual tmux required")
	}
	python, e := exec.LookPath("python3")
	if e != nil {
		t.Skip("actual PTY required")
	}
	goTool, e := exec.LookPath("go")
	if e != nil {
		t.Fatal(e)
	}
	root, e := os.MkdirTemp(os.Getenv("HOME"), "pt-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	binary := filepath.Join(root, "native-tmux-cli")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, goTool, "build", "-p", "2", "-tags", "native_process_fixture", "-o", binary, "../../cmd/claude-haha")
	// Compiler children may still hold the stdout pipe after the build parent
	// is canceled. Bound that join so a fixture timeout reports a build failure.
	build.WaitDelay = time.Second
	build.Env = os.Environ()
	capturePublicGoCoverage(t, build)
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatalf("actual public Go CLI build %v %.4000s", e, out)
	}
	for _, mode := range []string{"classic", "control", "existing", "nested", "nested-existing", "async", "async-rewake", "async-header"} {
		t.Run(mode, func(t *testing.T) {
			owned, e := os.MkdirTemp("/tmp", "go-tmux-pty-")
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { _ = os.RemoveAll(owned) })
			if e = os.WriteFile(filepath.Join(owned, "owned-tmux-pty"), []byte("private"), 0600); e != nil {
				t.Fatal(e)
			}
			cmd := exec.CommandContext(ctx, python, "testdata/tmux_public_pty.py", binary, owned, tmux, mode)
			cmd.WaitDelay = time.Second
			cmd.Env = []string{"HOME=" + owned, "TMPDIR=" + owned, "PATH=/usr/bin:/bin"}
			capturePublicGoCoverage(t, cmd)
			out, e := cmd.CombinedOutput()
			if e != nil {
				t.Fatalf("actual public PTY %s %v %.5000s", mode, e, out)
			}
			if !strings.Contains(string(out), `"exitCode": 0`) || !strings.Contains(string(out), `"terminalRestored": true`) || !strings.Contains(string(out), `"modelRequests": 0`) {
				t.Fatal(string(out))
			}
		})
	}
}
