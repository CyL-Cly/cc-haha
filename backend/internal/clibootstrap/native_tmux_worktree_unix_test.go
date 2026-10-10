//go:build !windows

package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestNativeTmuxFastPathTimedOutHookJoinsActualProcessBeforeReturning(t *testing.T) {
	f, _, _ := privateTmuxFastFixture(t)
	pidPath := filepath.Join(f.home, "actual-hook.pid")
	command := "printf '%s' \"$$\" > " + shellTmuxTest(pidPath) + "; exec /bin/sleep 30"
	raw, _ := json.Marshal(map[string]any{"hooks": map[string]any{"WorktreeCreate": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": 0.25}}}}}})
	f.write(t, filepath.Join(f.config, "settings.json"), string(raw))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out, stderr bytes.Buffer
	result, e := RunNativeTmuxWorktree(ctx, []string{"--tmux", "--worktree=timeout"}, f.n, runtime.GOOS, "/bin/sh", TmuxFastPathIO{Input: strings.NewReader(""), Output: &out, Error: &stderr})
	if e != nil || result.Handled || !strings.Contains(result.Error, "Hook cancelled") {
		t.Fatal(result, e, out.String(), stderr.String())
	}
	data, e := os.ReadFile(pidPath)
	if e != nil {
		t.Fatal("native hook did not actually start", e)
	}
	pid, e := strconv.Atoi(string(data))
	if e != nil {
		t.Fatal(e)
	}
	if e = syscall.Kill(pid, 0); !errors.Is(e, syscall.ESRCH) {
		t.Fatalf("actual timed-out hook PID %d escaped return: %v", pid, e)
	}
	if _, e = os.Stat(filepath.Join(f.config, "projects")); !os.IsNotExist(e) {
		t.Fatal("timeout created transcript", e)
	}
}
