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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
)

func TestActualCLIShiftTabCycleChangesToolsAndPlanContextPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY; ConPTY separate required")
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
	_ = os.WriteFile(filepath.Join(home, "disposable-mode-cycle"), []byte("private"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_mode_cycle_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_TUI_MODE_CYCLE_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("actual mode PTY %v %.6000s", e, out)
	}
	if !strings.Contains(string(out), `"terminalRestored": true`) {
		t.Fatal("restore proof absent", string(out))
	}
}

func TestNativeTUIModeCycleFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_TUI_MODE_CYCLE_HELPER") != "1" {
		t.Skip("owned native PTY helper")
	}
	home := os.Getenv("HOME")
	if _, e := os.Stat(filepath.Join(home, "disposable-mode-cycle")); e != nil {
		t.Fatal("private marker required")
	}
	var count atomic.Int32
	var planContext atomic.Bool
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
			response(w, "mode-write", "Write", map[string]any{"file_path": file, "content": "ACTUAL_MODE_ACCEPTED_WRITE_82\n"}, "")
		case 2:
			response(w, "mode-read", "Read", map[string]any{"file_path": file}, "")
		case 3:
			response(w, "mode-first", "", nil, "MODE_PTY_FIRST_DONE_82")
		case 4:
			planContext.Store(strings.Contains(strings.ToLower(string(body)), "plan mode"))
			response(w, "mode-plan-read", "Read", map[string]any{"file_path": file}, "")
		case 5:
			response(w, "mode-final", "", nil, "MODE_PTY_SECOND_DONE_82")
		default:
			t.Error("hotkeys caused extra inference", n)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Arguments.PermissionMode = "default"
	o.Arguments.Tools = []string{"Read,Write"}
	o.Stderr = os.Stderr
	file = filepath.Join(o.WorkDir, "mode-artifact.txt")
	before, e := term.GetState(os.Stdin.Fd())
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
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
	data, _ := os.ReadFile(file)
	global, _ := os.ReadFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], ".claude.json"))
	var saved map[string]json.RawMessage
	_ = json.Unmarshal(global, &saved)
	receipt, _ := json.Marshal(map[string]any{"requests": count.Load(), "physicalWrite": string(data) == "ACTUAL_MODE_ACCEPTED_WRITE_82\n", "planReminderReachedOriginalRequest": planContext.Load(), "originalPlanTimestampSaved": saved["lastPlanModeUse"] != nil, "terminalRestored": reflect.DeepEqual(before, after)})
	if e = os.WriteFile(filepath.Join(home, "mode-cycle-receipt.json"), receipt, 0600); e != nil {
		t.Fatal(e)
	}
}
