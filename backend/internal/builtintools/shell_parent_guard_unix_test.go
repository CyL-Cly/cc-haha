//go:build !windows

package builtintools

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestShellParentGuardMatchesFrozenMainScriptAndPreservesWaitArgsExit(t *testing.T) {
	source, err := os.ReadFile("../../../src/utils/shell/parentProcessGuard.ts")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(source), "const PARENT_PROCESS_GUARD = `")
	if start < 0 {
		t.Fatal("actual Source guardian missing")
	}
	start += len("const PARENT_PROCESS_GUARD = `")
	end := strings.IndexByte(string(source)[start:], '`')
	if end < 0 || string(source)[start:start+end] != parentProcessGuard {
		t.Fatal("native guardian differs from frozen Source script")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", `wait; printf '%s\n' "$1"; exit 3`, "target-shell", "unchanged literal; $argument")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir()}
	configureShellProcess(cmd)
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 || string(output) != "unchanged literal; $argument\n" || ctx.Err() != nil {
		t.Fatalf("guard changed args/bare wait/status: %q %v", output, err)
	}
}

func TestGoShellGuardRuntimeFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_NATIVE_SHELL_CRASH_FIXTURE") != "1" {
		return
	}
	home := os.Getenv("HOME")
	if home == "" || home == os.Getenv("CC_HAHA_FORBIDDEN_REAL_HOME") {
		t.Fatal("disposable HOME required")
	}
	file, err := os.OpenFile(filepath.Join(home, "ticks"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	cmd := exec.CommandContext(context.Background(), "/bin/sh", "-c", "while :; do echo alive; sleep 0.02; done")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home}
	cmd.Stdout = file
	configureShellProcess(cmd)
	capturePublicGoCoverage(t, cmd)
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "shell-pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Hour)
}

func TestShellGuardReapsProcessGroupAfterActualGoRuntimeSIGKILL(t *testing.T) {
	home := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runtime := exec.Command(binary, "-test.run=^TestGoShellGuardRuntimeFixture$")
	runtime.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, "config"), "TMPDIR=" + home, "CC_HAHA_NATIVE_SHELL_CRASH_FIXTURE=1"}
	capturePublicGoCoverage(t, runtime)
	if err = runtime.Start(); err != nil {
		t.Fatal(err)
	}
	defer runtime.Process.Kill()
	var shellPID int
	defer func() {
		if shellPID > 0 {
			_ = syscall.Kill(-shellPID, syscall.SIGKILL)
		}
	}()
	ticks := func() int {
		data, _ := os.ReadFile(filepath.Join(home, "ticks"))
		return strings.Count(string(data), "\n")
	}
	deadline := time.Now().Add(3 * time.Second)
	for ticks() < 3 {
		if time.Now().After(deadline) {
			t.Fatal("physical target shell did not write output")
		}
		time.Sleep(10 * time.Millisecond)
	}
	data, err := os.ReadFile(filepath.Join(home, "shell-pid"))
	if err != nil {
		t.Fatal(err)
	}
	shellPID, err = strconv.Atoi(string(data))
	if err != nil || shellPID <= 0 {
		t.Fatal(shellPID, err)
	}
	if err = runtime.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err = runtime.Wait(); err == nil {
		t.Fatal("fixture runtime wasn't SIGKILLed")
	}
	deadline = time.Now().Add(4 * time.Second)
	for {
		err = syscall.Kill(-shellPID, 0)
		if errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("guardian did not reap the runtime's detached group", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	last := ticks()
	time.Sleep(150 * time.Millisecond)
	if ticks() != last {
		t.Fatal("shell descendants survived the actual runtime crash")
	}
}
