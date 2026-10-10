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

func TestActualCLICommandMenuSelectedLocalAndRequiredSkillPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix native PTY; ConPTY separate required")
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
	if e = os.WriteFile(filepath.Join(home, "disposable-command-selection"), []byte("private"), 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_command_selection_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_TUI_COMMAND_SELECTION_HELPER=1"}
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("actual command selection PTY %v %.6000s", e, out)
	}
	if !strings.Contains(string(out), `"terminalRestored": true`) {
		t.Fatal("terminal restoration proof absent", string(out))
	}
}

func TestNativeTUICommandSelectionFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_TUI_COMMAND_SELECTION_HELPER") != "1" {
		t.Skip("owned private PTY helper")
	}
	home := os.Getenv("HOME")
	if _, e := os.Stat(filepath.Join(home, "disposable-command-selection")); e != nil {
		t.Fatal("private marker required")
	}
	var count atomic.Int32
	var promptOK atomic.Bool
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := count.Add(1)
		if n != 1 {
			t.Error("local command or editable required skill caused extra inference", n)
		}
		promptOK.Store(strings.Contains(string(body), "ARG_TARGET=a target; ARG_REGION=europe"))
		response(w, "command-selection", "", nil, "ACTUAL_COMMAND_CHOICE_DONE_9D")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Stderr = os.Stderr
	commands := filepath.Join(o.WorkDir, ".claude", "commands")
	if e := os.MkdirAll(commands, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(commands, "pty-args.md"), []byte("---\ndescription: Real required command arguments\narguments: [target, region]\nargument-hint: '<target> <region>'\n---\nARG_TARGET=$target; ARG_REGION=$region\n"), 0600); e != nil {
		t.Fatal(e)
	}
	before, e := term.GetState(os.Stdin.Fd())
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
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
	receipt, _ := json.Marshal(map[string]any{"requests": count.Load(), "actualNamedArguments": promptOK.Load(), "terminalRestored": reflect.DeepEqual(before, after)})
	if e = os.WriteFile(filepath.Join(home, "command-selection-receipt.json"), receipt, 0600); e != nil {
		t.Fatal(e)
	}
}
