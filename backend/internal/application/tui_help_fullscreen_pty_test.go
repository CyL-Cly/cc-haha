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

func TestActualCLIFullscreenHelpModalOuterScrollDismissalAndNextHumanPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native Unix PTY; ConPTY requires separate host")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err = os.WriteFile(filepath.Join(home, "owned-fullscreen-help"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_help_fullscreen_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "TMPDIR=" + home, "TERM=xterm-256color", "COLORTERM=truecolor", "CC_HAHA_FULLSCREEN_HELP_PTY=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), `"terminalRestored": true`) {
		t.Fatal("actual fullscreen Help public consumer", err, string(output))
	}
}

func TestNativeFullscreenHelpPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_FULLSCREEN_HELP_PTY") != "1" {
		t.Skip("owned private fullscreen Help fixture")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "owned-fullscreen-help")); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		ordinal := calls.Add(1)
		if ordinal > 3 || !strings.Contains(string(body), fmt.Sprintf("FULLSCREEN_HELP_HUMAN_%d", ordinal)) || strings.Contains(string(body), "<command-name>/help</command-name>") || strings.Contains(string(body), "Help dialog dismissed") || strings.Contains(string(body), "Claude understands your codebase") {
			t.Error("actual fullscreen completion or same Harness history differs", ordinal, string(body))
		}
		output := fmt.Sprintf("FULLSCREEN_HELP_DONE_%d", ordinal)
		if ordinal == 1 {
			rows := []string{}
			for i := 0; i < 70; i++ {
				rows = append(rows, fmt.Sprintf("FULLSCREEN_TRANSCRIPT_%03d", i))
			}
			output = strings.Join(rows, "\n") + "\n" + output
		}
		response(w, fmt.Sprintf("fullscreen-help-%d", ordinal), "", nil, output)
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive, o.ForegroundShellProgress = true, true
	o.Stderr = os.Stderr
	o.Environment["CLAUDE_CODE_NO_FLICKER"] = "1"
	o.Native.Environment["CLAUDE_CODE_NO_FLICKER"] = "1"
	commands := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "commands")
	if err := os.MkdirAll(commands, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		data := fmt.Sprintf("---\ndescription: Actual fullscreen custom command %02d\n---\nThis command is read-only in Help.\n", i)
		if err := os.WriteFile(filepath.Join(commands, fmt.Sprintf("owned-%02d.md", i)), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	var env []string
	for key, value := range o.Environment {
		env = append(env, key+"="+value)
	}
	env = append(env, "TERM=xterm-256color", "COLORTERM=truecolor")
	code, err := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, env)
	if err != nil || code != 0 {
		t.Fatal(code, err)
	}
	if _, err = nativeTUIPresentationTerminalProbe(os.Stdin.Fd()); err != nil {
		t.Fatal(err)
	}
	after, err := term.GetState(os.Stdin.Fd())
	if err != nil || calls.Load() != 3 {
		t.Fatal(err, calls.Load())
	}
	receipt, _ := json.Marshal(map[string]any{"code": code, "requests": calls.Load(), "terminalRestored": reflect.DeepEqual(before, after)})
	if err = os.WriteFile(filepath.Join(home, "fullscreen-help-receipt.json"), receipt, 0600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err = os.Stat(filepath.Join(home, "fullscreen-help-restoration-inspected")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("live leader restoration inspection missing", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
