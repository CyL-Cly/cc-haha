package application

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestActualCLIPTYToolOutputReadReminderErrorsTranscriptAndRestore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY; Windows requires separate ConPTY evidence")
	}
	python, e := exec.LookPath("python3")
	if e != nil {
		t.Skip("native PTY Python required")
	}
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	home := t.TempDir()
	if e = os.WriteFile(filepath.Join(home, "disposable-tui-presentation"), []byte("private fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_presentation_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_TUI_PRESENTATION_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("actual CLI PTY %v %.6000s", e, out)
	}
	if !strings.Contains(string(out), `"terminalRestored": true`) {
		t.Fatal("no restoration proof", string(out))
	}
}
func TestNativeTUIPresentationFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_TUI_PRESENTATION_HELPER") != "1" {
		t.Skip("owned private PTY helper")
	}
	home := os.Getenv("HOME")
	if _, e := os.Stat(filepath.Join(home, "disposable-tui-presentation")); e != nil {
		t.Fatal("private fixture marker required")
	}
	var count atomic.Int32
	var reminder atomic.Bool
	var localHistory atomic.Bool
	var file string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		n := count.Add(1)
		switch n {
		case 1:
			localHistory.Store(strings.Contains(string(body), "<bash-input>") && strings.Contains(string(body), "ACTUAL_TUI_LOCAL_SHELL_A") && strings.Contains(string(body), "local-command-caveat"))
			response(w, "actual-tui-bash", "Bash", map[string]any{"command": "printf 'RAW_TUI_LINE1\\nRAW_TUI_LINE2 中文\\n'", "description": "Actual readable output"}, "")
		case 2:
			response(w, "actual-tui-read", "Read", map[string]any{"file_path": file}, "")
		case 3:
			reminder.Store(strings.Contains(string(body), "system-reminder") && strings.Contains(string(body), "PRIVATE_ACTUAL_READ_1"))
			response(w, "actual-tui-failure", "Bash", map[string]any{"command": "for n in $(seq 1 13); do printf 'REAL_FAILURE_LINE_%02d\\n' \"$n\" >&2; done; exit 7", "description": "Actual failure output"}, "")
		case 4:
			if !strings.Contains(string(body), `"is_error":true`) {
				t.Error("physical tool error not delivered to original model")
			}
			response(w, "actual-tui-done", "", nil, "REAL_TUI_SHARED_HARNESS_DONE_Z")
		default:
			t.Errorf("unexpected request %d", n)
			http.Error(w, "unexpected", 400)
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Arguments.PermissionMode = "bypassPermissions"
	o.Arguments.Tools = []string{"Bash,Read"}
	o.Stderr = os.Stderr
	file = filepath.Join(o.WorkDir, "actual-read.txt")
	if e := os.WriteFile(file, []byte("PRIVATE_ACTUAL_READ_1\nPRIVATE_ACTUAL_READ_2"), 0600); e != nil {
		t.Fatal(e)
	}
	terminalBefore, e := term.GetState(os.Stdin.Fd())
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	code, e := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, os.Environ())
	if e != nil || code != 0 {
		t.Fatal("actual command", code, e)
	}
	beforeProbe, e := term.GetState(os.Stdin.Fd())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = nativeTUIPresentationTerminalProbe(os.Stdin.Fd()); e != nil {
		t.Fatal(e)
	}
	terminalAfter, e := term.GetState(os.Stdin.Fd())
	if e != nil {
		t.Fatal(e)
	}
	localArtifact, _ := os.ReadFile(filepath.Join(o.WorkDir, "actual-local-shell-artifact"))
	receipt, _ := json.Marshal(map[string]any{"localShellPhysicalArtifact": string(localArtifact) == "ACTUAL_TUI_LOCAL_FILE", "localShellHistoryReachedModel": localHistory.Load(), "requests": count.Load(), "readReminderReachedModel": reminder.Load(), "terminalRestored": reflect.DeepEqual(terminalBefore, terminalAfter), "terminalBefore": fmt.Sprintf("%#v", terminalBefore), "terminalAfter": fmt.Sprintf("%#v", terminalAfter), "beforeNativeProbe": fmt.Sprintf("%#v", beforeProbe)})
	if e = os.WriteFile(filepath.Join(home, "receipt.json"), receipt, 0600); e != nil {
		t.Fatal(e)
	}
}
