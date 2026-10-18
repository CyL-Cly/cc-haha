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

func TestActualPublicGoTUIExitAliasesAndCtrlDDrainSessionEndRestorePTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix native PTY; ConPTY separate evidence")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("Go toolchain required to build actual native fixture")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python PTY required")
	}
	buildRoot := t.TempDir()
	binary := filepath.Join(buildRoot, "claude-haha-exit")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, goTool, "build", "-tags", "native_process_fixture", "-o", binary, "../../cmd/claude-haha")
	// Test process environment is hermetic; the subprocess itself receives its
	// explicit private HOME/config/TMP and the tag isolates actual Keychain/MDM.
	build.Env = os.Environ()
	capturePublicGoCoverage(t, build)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("actual public CLI build %v %.4000s", err, out)
	}
	for _, mode := range []string{"exit", "quit", "ctrl-d", "after-read"} {
		t.Run(mode, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(root, "owned-exit-pty"), []byte("private"), 0600); err != nil {
				t.Fatal(err)
			}
			childCtx, stop := context.WithTimeout(ctx, 35*time.Second)
			defer stop()
			cmd := exec.CommandContext(childCtx, python, "testdata/tui_exit_public_pty.py", binary, root, mode)
			cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + root, "TMPDIR=" + root}
			capturePublicGoCoverage(t, cmd)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("actual public CLI %s %v %.5000s", mode, err, out)
			}
			requestEvidence := `"modelRequests": 0`
			if mode == "after-read" {
				requestEvidence = `"modelRequests": 2`
				if !strings.Contains(string(out), `"physicalRead": true`) {
					t.Fatal("actual Read missing from next POST", string(out))
				}
			}
			if !strings.Contains(string(out), `"exitCode": 0`) || !strings.Contains(string(out), `"sessionEndCount": 1`) || !strings.Contains(string(out), `"reason": "prompt_input_exit"`) || !strings.Contains(string(out), requestEvidence) || !strings.Contains(string(out), `"terminalRestored": true`) {
				t.Fatal("physical exit evidence absent", string(out))
			}
		})
	}
}
