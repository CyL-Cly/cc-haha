package cloudauth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestConfiguredCloudCommandUsesPrivateEnvironmentExitAndTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix physical shell fixture")
	}
	home := t.TempDir()
	env := map[string]string{"HOME": home, "PATH": "/usr/bin:/bin", "CLOUD_PRIVATE_MARKER": "private-literal"}
	value, err := RunConfiguredCommand(context.Background(), `printf '%s\n' "$CLOUD_PRIVATE_MARKER"; printf 'private stderr\n' >&2; exit 7`, 0, home, env)
	if err != nil || value.ExitCode != 7 || value.Stdout != "private-literal" || value.Stderr != "private stderr" {
		t.Fatal(value, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	marker := filepath.Join(home, "auth-started")
	// POSIX shell builtins publish readiness before exec preserves this exact
	// PID as the sleeping process. Python startup is unrelated to timeout/join.
	command := `printf '%s\n' "$$" > auth-pid; printf 'started' > auth-started; exec /bin/sleep 30`
	started := time.Now()
	value, err = RunConfiguredCommand(ctx, command, 250*time.Millisecond, home, env)
	if err != nil {
		t.Fatal(err)
	}
	if value.ExitCode == 0 || time.Since(started) > 3*time.Second {
		t.Fatal("native configured command did not timeout and join", value, time.Since(started))
	}
	if _, err = os.Stat(marker); err != nil {
		t.Fatal("actual private process did not start", err)
	}
	rawPID, err := os.ReadFile(filepath.Join(home, "auth-pid"))
	if err != nil {
		t.Fatal("private timeout process PID missing", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(rawPID)))
	if err != nil || pid <= 1 || pid == os.Getpid() {
		t.Fatal("invalid actual private process PID", string(rawPID), err)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	defer process.Release()
	if err = process.Signal(syscall.Signal(0)); !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		t.Fatal("timed out process survived actual join", pid, err)
	}
}
