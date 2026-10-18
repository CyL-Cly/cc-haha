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

func TestActualPublicTUIResumeInitialHistoryAndOrdinaryExitScrollback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY; ConPTY needs separate evidence")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "claude-haha-history")
	build := exec.CommandContext(ctx, goTool, "build", "-tags", "native_process_fixture", "-o", binary, "../../cmd/claude-haha")
	build.Env = os.Environ()
	capturePublicGoCoverage(t, build)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("actual public CLI build %v %.4000s", err, out)
	}
	for _, mode := range []string{"ordinary", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "owned-history-pty"), []byte("private"), 0600); err != nil {
				t.Fatal(err)
			}
			childCtx, stop := context.WithTimeout(ctx, 35*time.Second)
			defer stop()
			cmd := exec.CommandContext(childCtx, python, "testdata/tui_history_public_pty.py", binary, root, mode)
			cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + root, "TMPDIR=" + root}
			capturePublicGoCoverage(t, cmd)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("actual public TUI history %v %.7000s", err, out)
			}
			for _, proof := range []string{`"initialHistoryVisible": true`, `"startupModelRequests": 0`, `"startupCanonicalUnchanged": true`, `"exitCode": 0`, `"terminalRestored": true`, `"screenContractRetained": true`, `"resumeHintOnMainBuffer": true`, `"physicalRead": true`, `"modelRequests": 3`} {
				if !strings.Contains(string(out), proof) {
					t.Fatal("public PTY evidence absent", proof, string(out))
				}
			}
			t.Log(strings.TrimSpace(string(out)))
		})
	}
}
