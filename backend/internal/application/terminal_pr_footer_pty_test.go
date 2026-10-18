package application

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
	"github.com/charmbracelet/x/term"
)

func TestActualCLIReadOnlyPRFooterSavedToggleNextModelFreshSessionAndHeldProcessExitPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY; ConPTY separate")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("native PTY unavailable")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	_ = os.WriteFile(filepath.Join(home, "disposable-pr-pty"), []byte("owned"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/terminal_pr_footer_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_PR_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual PR footer PTY %v %.7000s", err, output)
	}
	if !strings.Contains(string(output), `"terminalRestored": true`) {
		t.Fatal(string(output))
	}
}

func TestNativeCLIReadOnlyPRFooterPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_PR_PTY_HELPER") != "1" {
		t.Skip("owned native PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-pr-pty")); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	var artifact string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		switch requests.Add(1) {
		case 1:
			response(w, "pr-footer-write", "Write", map[string]any{"file_path": artifact, "content": "PUBLIC_PR_FOOTER_SAME_HARNESS"}, "")
		case 2:
			response(w, "pr-footer-done", "", nil, "PR_FOOTER_FIRST_DONE_872")
		case 3:
			response(w, "pr-footer-fresh", "", nil, "PR_FOOTER_FRESH_DONE_872")
		default:
			t.Error("unexpected model request")
			http.Error(w, "extra", 500)
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	o.Arguments.PermissionMode = "acceptEdits"
	o.Arguments.Tools = []string{"Read,Write"}
	artifact = filepath.Join(o.WorkDir, "pr-footer-artifact.txt")
	ctx, cancel := context.WithTimeout(context.Background(), 34*time.Second)
	defer cancel()
	for _, args := range [][]string{{"init", "--initial-branch=private-pr-footer"}, {"symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk"}} {
		r, err := worktree.NativeRun(ctx, worktree.ProcessRequest{Command: worktree.NativeGitExecutable(o.Environment), Arguments: args, CWD: o.WorkDir, Environment: o.Environment, IgnoreStdin: true})
		if err != nil || r.Code != 0 {
			t.Fatal("private Git setup", err, r.Stderr)
		}
	}
	bin := filepath.Join(home, "private-bin")
	_ = os.MkdirAll(bin, 0700)
	gh := filepath.Join(bin, "gh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$PWD\" \"$HOME\" \"$*\" >> \"$PR_NATIVE_RECEIPT\"\nif [ -f \"$PR_NATIVE_HOLD\" ]; then printf '%s' \"$$\" > \"$PR_NATIVE_PID\"; exec /bin/sleep 30; fi\nprintf '%s' '{\"number\":812,\"url\":\"https://example.test/pr/812\",\"reviewDecision\":\"APPROVED\",\"isDraft\":false,\"headRefName\":\"private-pr-footer\",\"state\":\"OPEN\"}'\n"
	if err := os.WriteFile(gh, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["PATH"] = bin + ":" + env["PATH"]
		env["PR_NATIVE_RECEIPT"] = filepath.Join(home, "pr-gh-receipt")
		env["PR_NATIVE_HOLD"] = filepath.Join(home, "hold-gh")
		env["PR_NATIVE_PID"] = filepath.Join(home, "pr-held-pid")
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.MkdirAll(config, 0700)
	globalPath := filepath.Join(config, ".claude.json")
	_ = os.WriteFile(globalPath, raw(map[string]any{"hasCompletedOnboarding": true, "futurePR": map[string]any{"keep": true}, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}), 0600)
	_ = os.WriteFile(filepath.Join(home, "pr-paths.json"), raw(map[string]any{"global": globalPath, "artifact": artifact, "ghReceipt": o.Environment["PR_NATIVE_RECEIPT"], "heldPID": o.Environment["PR_NATIVE_PID"]}), 0600)
	for phase := 1; phase <= 2; phase++ {
		if phase == 2 {
			fmt.Fprintln(os.Stdout, "PR_FOOTER_SECOND_CLI_START_872")
		}
		before, err := term.GetState(os.Stdin.Fd())
		if err != nil {
			t.Fatal(err)
		}
		code, err := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, os.Environ())
		if err != nil || code != 0 {
			t.Fatal(code, err)
		}
		if _, err = nativeTUIPresentationTerminalProbe(os.Stdin.Fd()); err != nil {
			t.Fatal(err)
		}
		after, err := term.GetState(os.Stdin.Fd())
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(globalPath)
		if err != nil {
			t.Fatal(err)
		}
		var global protocol.JSONObject
		_ = global.UnmarshalJSON(data)
		future, _ := global.Object("futurePR")
		receipt := map[string]any{"terminalRestored": reflect.DeepEqual(before, after), "requests": requests.Load(), "unknownFieldsPreserved": string(future.Get("keep")) == "true", "exitCode": code}
		_ = os.WriteFile(filepath.Join(home, fmt.Sprintf("pr-receipt-%d.json", phase)), raw(receipt), 0600)
		deadline := time.Now().Add(3 * time.Second)
		for {
			if _, err = os.Stat(filepath.Join(home, fmt.Sprintf("pr-release-%d", phase))); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("actual terminal inspection acknowledgement missing")
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatal(receipt)
		}
	}
	if requests.Load() != 3 {
		t.Fatal("local PR footer queried model", requests.Load())
	}
}
