package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMCPClientSecretNativeTTYUnicodeBackspaceAndCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY; Windows terminal separate")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("PTY driver unavailable")
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err = os.WriteFile(filepath.Join(home, "disposable-mcp-secret-pty"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cli-mcp-secret-tty.fixture.ts")
	cmd := exec.CommandContext(ctx, python, "testdata/mcp_secret_pty.py", binary, bun, fixture, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_MCP_SECRET_PTY_HELPER=1", "CC_HAHA_CLI_MCP_SECRET_TTY_FIXTURE=1"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal("actual MCP secret PTY", err, string(out))
	}
	if !strings.Contains(string(out), `"unicodeBackspace": true`) {
		t.Fatal(string(out))
	}
}

func TestMCPClientSecretTTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_MCP_SECRET_PTY_HELPER") != "1" {
		t.Skip("owned private MCP secret helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-mcp-secret-pty")); err != nil {
		t.Fatal(err)
	}
	secret, err := readMCPClientSecretJSON(context.Background(), map[string]string{}, os.Stdin, os.Stderr)
	failure := ""
	if err != nil {
		failure = err.Error()
		secret = json.RawMessage(`""`)
	}
	value, _ := json.Marshal(map[string]any{"secret": secret, "failure": failure})
	if err = os.WriteFile(filepath.Join(home, "native-secret-receipt.json"), value, 0600); err != nil {
		t.Fatal(err)
	}
}
