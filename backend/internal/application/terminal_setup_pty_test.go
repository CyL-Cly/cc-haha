package application

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

	"github.com/charmbracelet/x/term"
)

func TestActualCLITerminalSetupConfigurationBackupNextHumanAndExitPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix physical PTY; native Windows machine required")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	binary, _ := os.Executable()
	home := t.TempDir()
	if err = os.WriteFile(filepath.Join(home, "private-terminal-setup-pty"), []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, "testdata/terminal_setup_pty.py", binary, home)
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=" + os.Getenv("PATH"), "TERM=xterm-256color", "CC_HAHA_TERMINAL_SETUP_PTY_HELPER=1"}
	capturePublicGoCoverage(t, command)
	data, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(data), `"terminalRestored": true`) {
		t.Fatalf("actual terminal setup PTY %v %.6000s", err, data)
	}
}

func TestTerminalSetupPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_TERMINAL_SETUP_PTY_HELPER") != "1" {
		t.Skip("owned terminal setup PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "private-terminal-setup-pty")); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	var canonical atomic.Bool
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		data, _ := io.ReadAll(r.Body)
		calls.Add(1)
		canonical.Store(strings.Contains(string(data), "Installed VSCode terminal Shift+Enter key binding") && strings.Contains(string(data), "Found existing VSCode terminal Shift+Enter key binding. Remove it to continue."))
		response(w, "terminal-setup-next-public", "", nil, "TERMINAL_SETUP_NEXT_PUBLIC_379B")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Stderr = os.Stderr
	seedTerminalReleaseWarmFixture(t, o)
	for key, value := range map[string]string{"TERM_PROGRAM": "vscode", "NO_COLOR": "1", "NO_PROXY": "127.0.0.1,localhost"} {
		o.Environment[key] = value
		o.Native.Environment[key] = value
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	code, err := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, os.Environ())
	if err != nil || code != 0 {
		t.Fatal("actual RunCommand result", code, err)
	}
	rawAfter, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	// XNU's restored canonical input queue sets PENDIN until its native
	// FIONREAD observation. Preserve that raw snapshot; consume no bytes.
	if _, err = nativeTUIPresentationTerminalProbe(os.Stdin.Fd()); err != nil {
		t.Fatal(err)
	}
	after, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(o.Native.Home, "Library", "Application Support", "Code", "User", "keybindings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var bindings []struct {
		Key, Command, When string
		Args               struct{ Text string }
	}
	if json.Unmarshal(data, &bindings) != nil || len(bindings) != 1 || bindings[0].Key != "shift+enter" || bindings[0].Command != "workbench.action.terminal.sendSequence" || bindings[0].When != "terminalFocus" || bindings[0].Args.Text != "\x1b\r" {
		t.Fatal("actual editor shortcut file", string(data))
	}
	backups, err := filepath.Glob(path + ".*.bak")
	if err != nil || len(backups) != 1 {
		t.Fatal("original duplicate-check backup consumer", backups, err)
	}
	backup, err := os.ReadFile(backups[0])
	if err != nil || string(backup) != string(data) {
		t.Fatal("physical backup changed original bytes", err)
	}
	receipt := map[string]any{"requests": calls.Load(), "canonical": canonical.Load(), "configurationWritten": true, "backupRetained": true, "terminalRestored": reflect.DeepEqual(before, after), "before": fmt.Sprintf("%#v", before), "rawAfter": fmt.Sprintf("%#v", rawAfter), "after": fmt.Sprintf("%#v", after)}
	if err = os.WriteFile(filepath.Join(home, "terminal-setup-pty-receipt.json"), raw(receipt), 0600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err = os.Stat(filepath.Join(home, "terminal-setup-termios-release")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("physical terminal restoration was not released")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
