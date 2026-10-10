package clilauncher

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPhysicalLauncherPreservesArgumentsPortableConfigAndExistingShellContent(t *testing.T) {
	home := t.TempDir()
	bundle := filepath.Join(home, "bundle's path")
	_ = os.MkdirAll(bundle, 0700)
	source := filepath.Join(bundle, "claude-sidecar-go-fixture")
	capture := filepath.Join(home, "argv.txt")
	config := filepath.Join(home, "portable's config")
	script := `#!/bin/sh
printf '%s\n' "$CLAUDE_CONFIG_DIR" "$@" > "$CLI_LAUNCHER_CAPTURE"
printf 'actual fixture sidecar\n'
exit 0
`
	if e := os.WriteFile(source, []byte(script), 0755); e != nil {
		t.Fatal(e)
	}
	profile := filepath.Join(home, ".zshrc")
	if e := os.WriteFile(profile, []byte("export CUSTOM_VALUE='keep me'\n"), 0600); e != nil {
		t.Fatal(e)
	}
	env := map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": config, "SHELL": "/bin/zsh", "PATH": "/usr/bin:/bin", "CLI_LAUNCHER_CAPTURE": capture}
	s, e := New(Options{HomeDir: home, Executable: source, Platform: "darwin", Environment: func() map[string]string { return env }})
	if e != nil {
		t.Fatal(e)
	}
	status := s.Ensure(context.Background())
	if !status.Installed || !status.PathConfigured || status.PathInCurrentShell || !status.NeedsTerminalRestart || status.LastError != nil {
		t.Fatalf("actual install%+v", status)
	}
	first, e := os.ReadFile(profile)
	if e != nil || !strings.Contains(string(first), "CUSTOM_VALUE='keep me'") {
		t.Fatal(e, string(first))
	}
	s.Ensure(context.Background())
	second, e := os.ReadFile(profile)
	if e != nil || string(first) != string(second) || strings.Count(string(second), pathStart) != 1 {
		t.Fatal("repeated PATH setup rewrote unrelated fields", e, string(second))
	}
	cmd := exec.Command(status.LauncherPath, "--version", "quoted argument", `literal $() \" ' 中文`)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "PATH=/usr/bin:/bin", "CLAUDE_CONFIG_DIR=incorrect", "CLI_LAUNCHER_CAPTURE=" + capture}
	output, e := cmd.CombinedOutput()
	if e != nil || string(output) != "actual fixture sidecar\n" {
		t.Fatal(e, string(output))
	}
	data, e := os.ReadFile(capture)
	expected := strings.Join([]string{config, "cli", "--app-root", bundle, "--version", "quoted argument", `literal $() \" ' 中文`, ""}, "\n")
	if e != nil || string(data) != expected {
		t.Fatalf("actual wrapper changed arguments/config error%v got%q want%q", e, data, expected)
	}
}
func TestUnsupportedLauncherDoesNotWriteProfilesOrBin(t *testing.T) {
	home := t.TempDir()
	env := map[string]string{"HOME": home, "PATH": "/usr/bin:/bin", "CLAUDE_CLI_PATH": filepath.Join(home, "cli.ts")}
	s, e := New(Options{HomeDir: home, Platform: "darwin", Environment: func() map[string]string { return env }})
	if e != nil {
		t.Fatal(e)
	}
	status := s.Ensure(context.Background())
	if status.Supported || status.Installed {
		t.Fatal(status)
	}
	entries, e := os.ReadDir(home)
	if e != nil || len(entries) != 0 {
		t.Fatal("unsupported launcher wrote state", e, entries)
	}
}
