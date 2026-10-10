//go:build !windows

package mcpclient

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestOwnedStubbornMCPProcess(t *testing.T) {
	mode := os.Getenv("CC_HAHA_MCP_STUBBORN")
	if mode == "" {
		return
	}
	signal.Ignore(syscall.SIGINT, syscall.SIGTERM)
	if mode == "parent" {
		executable, _ := os.Executable()
		cmd := exec.Command(executable, "-test.run=^TestOwnedStubbornMCPProcess$")
		cmd.Env = []string{"CC_HAHA_MCP_STUBBORN=child", "HOME=" + os.Getenv("HOME")}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if cmd.Start() != nil {
			os.Exit(3)
		}
		_ = os.WriteFile(os.Getenv("CC_HAHA_MCP_CHILD_PID"), []byte(strconv.Itoa(cmd.Process.Pid)), 0600)
	}
	for {
		time.Sleep(time.Hour)
	}
}

func TestStdioCancellationKillsOwnedProcessTree(t *testing.T) {
	home := t.TempDir()
	executable, _ := os.Executable()
	pidFile := filepath.Join(home, "child.pid")
	o := options(home)
	o.ConnectTimeout = 80 * time.Millisecond
	_, err := Connect(context.Background(), Config{Type: "stdio", Command: executable, Args: []string{"-test.run=^TestOwnedStubbornMCPProcess$"}, Env: map[string]string{"CC_HAHA_MCP_STUBBORN": "parent", "CC_HAHA_MCP_CHILD_PID": pidFile}}, o)
	if err == nil {
		t.Fatal("unresponsive child connected")
	}
	pidText, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(string(pidText))
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A killed orphan can briefly remain a zombie under a slow test host's PID 1.
	// Verify that it no longer executes or owns the MCP pipes using ps state.
	state, _ := exec.Command("/bin/ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if len(state) > 0 && state[0] == 'Z' {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("owned descendant survived: pid=%d state=%s", pid, state)
}

func TestStdioUsesResolvedChildPATH(t *testing.T) {
	home := t.TempDir()
	executable, _ := os.Executable()
	directory := filepath.Join(home, "commands")
	_ = os.Mkdir(directory, 0700)
	if err := os.Symlink(executable, filepath.Join(directory, "owned-mcp")); err != nil {
		t.Fatal(err)
	}
	config := childConfig(t, home)
	config.Command = "owned-mcp"
	config.Env["PATH"] = directory
	c := mustConnect(t, config, options(home))
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	config.Command = "absent-owned-mcp"
	if _, err := Connect(context.Background(), config, options(home)); err == nil {
		t.Fatal("missing command silently connected")
	}
}
