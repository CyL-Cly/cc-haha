package application

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
)

func TestActualCLITrustedKeybindingChordHotReloadAndNextHumanPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY; Windows ConPTY requires its own actual host")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("native Python PTY unavailable")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "disposable-keybindings-pty"), []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/keybindings_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_KEYBINDING_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal("real public keybinding PTY failed", err, string(output))
	}
	if !strings.Contains(string(output), `"terminalRestored": true`) {
		t.Fatal("PTY restoration receipt missing", string(output))
	}
}

func TestNativeCLITrustedKeybindingPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_KEYBINDING_PTY_HELPER") != "1" {
		t.Skip("owned isolated public-CLI PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-keybindings-pty")); err != nil {
		t.Fatal("isolated helper marker missing")
	}
	var requests, gateCalls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		requests.Add(1)
		if !strings.Contains(string(body), "KEYBINDING_NEXT_HUMAN") || strings.Count(string(body), "<command-name>/help</command-name>") != 1 || strings.Count(string(body), "<local-command-stdout>Help dialog dismissed</local-command-stdout>") != 1 || strings.Contains(string(body), "<command-name>/skills</command-name>") || strings.Contains(string(body), "<command-name>/permissions</command-name>") {
			t.Error("actual Source Help canonical replay or normal next input differs", string(body))
		}
		response(w, "keybinding-next", "", nil, "KEYBINDING_PUBLIC_NEXT_COMPLETE")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Stderr = os.Stderr
	file := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "keybindings.json")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	initial := []byte(`{"future":{"untouched":17},"bindings":[{"context":"Chat","bindings":{"alt+p":null,"ctrl+k ctrl+p":"command:help"}}]}`)
	if err := os.WriteFile(file, initial, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "keybinding-path.txt"), []byte(file), 0600); err != nil {
		t.Fatal(err)
	}
	o.KeybindingCustomizationEnabled = func() bool {
		count := gateCalls.Add(1)
		_ = os.WriteFile(filepath.Join(home, "keybinding-gate-calls.txt"), []byte(strconv.Itoa(int(count))), 0600)
		return true
	}
	work, err := filepath.EvalSymlinks(o.WorkDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(file), ".claude.json"), raw(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{work: map[string]any{"hasTrustDialogAccepted": true}}}), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	code, err := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, os.Environ())
	if err != nil || code != 0 {
		t.Fatal("public CLI did not exit normally", code, err)
	}
	if _, err = nativeTUIPresentationTerminalProbe(os.Stdin.Fd()); err != nil {
		t.Fatal(err)
	}
	after, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	contents, err := os.ReadFile(file)
	if err != nil || json.Unmarshal(contents, &saved) != nil {
		t.Fatal("keybindings file was not preserved", err)
	}
	receipt := map[string]any{"terminalRestored": reflect.DeepEqual(before, after), "requests": requests.Load(), "gateCalls": gateCalls.Load(), "exitCode": code, "unknownField": saved["future"]}
	if err := os.WriteFile(filepath.Join(home, "keybinding-receipt.json"), raw(receipt), 0600); err != nil {
		t.Fatal(err)
	}
	// Keep the controlling process alive while the parent checks the physical
	// slave terminal; XNU invalidates its ioctl target after the last child exits.
	ackDeadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(home, "keybinding-pty-release")); err == nil {
			break
		}
		if time.Now().After(ackDeadline) {
			t.Fatal("physical terminal inspection acknowledgement missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if requests.Load() != 1 || gateCalls.Load() < 4 {
		t.Fatal("local keybinding path did not remain local and consume its actual reload", receipt)
	}
}
