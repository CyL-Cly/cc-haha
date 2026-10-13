//go:build windows

package ideintegration

import (
	"context"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func nativeProcessRunning(pid int) bool {
	if pid < 0 {
		return false
	}
	handle, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if e != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var code uint32
	return windows.GetExitCodeProcess(handle, &code) == nil && code == 259
}
func nativeExecutableMode(mode os.FileMode) bool { return !mode.IsDir() }
func configureProcess(command *exec.Cmd) {
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		root := ""
		for _, item := range command.Env {
			k, v, _ := strings.Cut(item, "=")
			if strings.EqualFold(k, "SystemRoot") {
				root = v
				break
			}
		}
		if root != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			killer := exec.CommandContext(ctx, filepath.Join(root, "System32", "taskkill.exe"), "/PID", strconv.Itoa(command.Process.Pid), "/T", "/F")
			killer.Env = command.Env
			killer.WaitDelay = 250 * time.Millisecond
			if killer.Run() == nil {
				return nil
			}
		}
		return command.Process.Kill()
	}
}
