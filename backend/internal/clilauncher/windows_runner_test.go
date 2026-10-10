package clilauncher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWindowsLauncherReportsActualPathRunnerFailureAndReplacesLegacyExecutable(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(bin, "claude-haha.exe")
	if err := os.WriteFile(legacy, []byte("old copied executable"), 0700); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": home, "USERPROFILE": home, "PATH": `C:\Windows`, "CLAUDE_CLI_PATH": filepath.Join(home, "claude-sidecar-go.exe")}
	var commands [][]string
	s, err := New(Options{HomeDir: home, Platform: "windows", Environment: func() map[string]string { return env }, Run: func(_ context.Context, name string, args []string) ExecResult {
		commands = append(commands, append([]string{name}, args...))
		if args[len(args)-1] == "[Environment]::GetEnvironmentVariable('Path', 'User')" {
			return ExecResult{Code: 1, Error: errors.New("read denied")}
		}
		return ExecResult{Code: 1, Stderr: "\r\n PATH update denied \r\n"}
	}})
	if err != nil {
		t.Fatal(err)
	}
	status := s.Ensure(context.Background())
	if !status.Supported || !status.Installed || status.PathConfigured || status.AvailableInNewTerminals || status.NeedsTerminalRestart || status.LastError == nil || *status.LastError != "PATH update denied" || status.ConfigTarget == nil || *status.ConfigTarget != "Windows User PATH" {
		t.Fatalf("runner error was hidden behind installed launcher: %+v", status)
	}
	if len(commands) != 2 || !reflect.DeepEqual(commands[1], []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command", windowsPathScript}) {
		t.Fatal("PATH mutation bypassed original PowerShell boundary", commands)
	}
	data, err := os.ReadFile(status.LauncherPath)
	if err != nil || string(data) != WindowsWrapper(env["CLAUDE_CLI_PATH"], "") {
		t.Fatal("Windows command launcher not installed", err, string(data))
	}
	if _, err = os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("old CLI executable still shadows command wrapper", err)
	}
}

func TestWindowsExistingUserPathDoesNotRunMutation(t *testing.T) {
	home := t.TempDir()
	env := map[string]string{"HOME": home, "PATH": "", "CLAUDE_CLI_PATH": filepath.Join(home, "claude-sidecar-go.exe")}
	calls := 0
	s, err := New(Options{HomeDir: home, Platform: "windows", Environment: func() map[string]string { return env }, Run: func(_ context.Context, _ string, _ []string) ExecResult {
		calls++
		return ExecResult{Stdout: filepath.Join(home, ".local", "bin") + "\r\n"}
	}})
	if err != nil {
		t.Fatal(err)
	}
	status := s.Ensure(context.Background())
	if calls != 1 || !status.AvailableInNewTerminals || !status.NeedsTerminalRestart || status.LastError != nil {
		t.Fatal("existing PATH was changed or hidden", calls, status)
	}
}
