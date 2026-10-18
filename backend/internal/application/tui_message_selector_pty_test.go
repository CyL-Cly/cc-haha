package application

import (
	"context"
	"encoding/json"
	"github.com/charmbracelet/x/term"
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
)

func TestActualCLIMessageSelectorPartialCompactPTY(t *testing.T) {
	runActualMessageSelectorPTY(t, "double-escape")
}

func TestActualCLIRewindAliasesMessageSelectorPTY(t *testing.T) {
	for _, entry := range []string{"rewind", "checkpoint"} {
		t.Run(entry, func(t *testing.T) { runActualMessageSelectorPTY(t, entry) })
	}
}

func runActualMessageSelectorPTY(t *testing.T, entry string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("native Unix PTY, ConPTY separate")
	}
	python, e := exec.LookPath("python3")
	if e != nil {
		t.Skip("native Python PTY required")
	}
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	home := t.TempDir()
	_ = os.WriteFile(filepath.Join(home, "disposable-message-selector"), []byte("private"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_message_selector_pty.py", binary, home, entry)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_TUI_MESSAGE_SELECTOR_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("native selector PTY %v %.6000s", e, out)
	}
	if !strings.Contains(string(out), `"terminalRestored": true`) {
		t.Fatal(string(out))
	}
}
func TestNativeTUIMessageSelectorFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_TUI_MESSAGE_SELECTOR_HELPER") != "1" {
		t.Skip("owned private PTY helper")
	}
	home := os.Getenv("HOME")
	if _, e := os.Stat(filepath.Join(home, "disposable-message-selector")); e != nil {
		t.Fatal("private marker required")
	}
	var count atomic.Int32
	var feedback, continuation atomic.Bool
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		switch n := count.Add(1); n {
		case 1:
			response(w, "selector-pty-before", "", nil, "SELECTOR_NATIVE_ORIGINAL_DONE")
		case 2:
			feedback.Store(strings.Contains(string(body), "preserve original explicit request") && strings.Contains(string(body), "CRITICAL: Respond with TEXT ONLY"))
			response(w, "selector-pty-summary", "", nil, "<summary>SELECTOR_NATIVE_SUMMARY_STATE</summary>")
		case 3:
			continuation.Store(strings.Contains(string(body), "SELECTOR_NATIVE_SUMMARY_STATE") && strings.Contains(string(body), "Original authored task for native selector"))
			response(w, "selector-pty-after", "", nil, "SELECTOR_NATIVE_CONTINUED_DONE")
		default:
			t.Error("unexpected model count", n)
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Stderr = os.Stderr
	o.Environment["DISABLE_AUTO_COMPACT"] = "1"
	before, e := term.GetState(os.Stdin.Fd())
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	code, e := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, os.Environ())
	if e != nil || code != 0 {
		t.Fatal(code, e)
	}
	if _, e = nativeTUIPresentationTerminalProbe(os.Stdin.Fd()); e != nil {
		t.Fatal(e)
	}
	after, e := term.GetState(os.Stdin.Fd())
	if e != nil {
		t.Fatal(e)
	}
	receipt, _ := json.Marshal(map[string]any{"requests": count.Load(), "feedbackReachedModel": feedback.Load(), "sameSessionContinuation": continuation.Load(), "terminalRestored": reflect.DeepEqual(before, after)})
	if e = os.WriteFile(filepath.Join(home, "message-selector-receipt.json"), receipt, 0600); e != nil {
		t.Fatal(e)
	}
	// The controlling-session leader remains alive until Python can query
	// the actual slave flags; macOS detaches that ioctl after leader exit.
	for {
		if _, e = os.Stat(filepath.Join(home, "message-selector-termios-release")); e == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("physical terminal restoration not released")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
