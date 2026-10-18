package application

import (
	"bytes"
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

func TestActualRunCommandTaskListAutoExpandHelpUnmountResetNextHumanAndTermiosPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY; ConPTY separate")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Native Python PTY required")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err = os.WriteFile(filepath.Join(home, "disposable-task-list-pty"), []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_task_list_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "TMPDIR=" + home, "TERM=xterm-256color", "COLORTERM=truecolor", "CC_HAHA_TUI_TASK_LIST_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		screen, _ := os.ReadFile(filepath.Join(home, "task-list-final-screen.txt"))
		journal, _ := os.ReadFile(filepath.Join(home, "task-list-terminal.txt"))
		// SIGQUIT diagnostics in the driver retain the complete blocked
		// process stack; a tail alone can omit the owning continuation.
		t.Logf("Actual task-list failure journal:\n%s", journal)
		t.Fatalf("Actual task-list public %v %s screen%s tail%s", err, output, screen, journal[max(0, len(journal)-6000):])
	}
	if !bytes.Contains(output, []byte(`"terminalRestored": true`)) {
		t.Fatal(string(output))
	}
}

func TestNativeTaskListPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_TUI_TASK_LIST_HELPER") != "1" {
		t.Skip("Owned private PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-task-list-pty")); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(request.Body)
		n := calls.Add(1)
		if n > 1 && bytes.Contains(body, []byte(`"is_error":true`)) {
			t.Errorf("Actual Task tool error %s", body)
		}
		if n == 7 {
			messages := obj(body).Get("messages")
			for _, needle := range []string{"TASK_LIST_HUMAN_1", "TASK_LIST_HUMAN_2", "TASK_LIST_HUMAN_3", "task-create-1", "task-complete-2"} {
				if !strings.Contains(string(messages), needle) {
					t.Errorf("Next actual Harness lost %s: %s", needle, messages)
				}
			}
			if strings.Count(string(messages), "<command-name>/help</command-name>") != 1 || strings.Count(string(messages), "<local-command-stdout>Help dialog dismissed</local-command-stdout>") != 1 {
				t.Errorf("Ordinary Help canonical differs from Source: %s", messages)
			}
		}
		switch n {
		case 1, 2:
			response(w, fmt.Sprintf("task-create-%d", n), "TaskCreate", map[string]any{"subject": fmt.Sprintf("PUBLIC_TASK_SUBJECT_%d", n), "description": "actual public durable task"}, "")
		case 3:
			response(w, "task-list-done-1", "", nil, "TASK_LIST_DONE_1")
		case 4, 5:
			response(w, fmt.Sprintf("task-complete-%d", n-3), "TaskUpdate", map[string]any{"taskId": fmt.Sprint(n - 3), "status": "completed"}, "")
		case 6:
			response(w, "task-list-done-2", "", nil, "TASK_LIST_DONE_2")
		case 7:
			response(w, "task-list-done-3", "", nil, "TASK_LIST_DONE_3")
		default:
			t.Errorf("Unexpected actual model request%d %s", n, body)
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive, o.ForegroundShellProgress = true, true
	o.Arguments.Tools = []string{"TaskCreate,TaskUpdate"}
	o.Stderr = os.Stderr
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(config, ".claude.json")
	if err := os.WriteFile(global, []byte(`{"hasCompletedOnboarding":true,"showExpandedTodos":false,"showSpinnerTree":false,"taskListFuture":{"keep":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"task-list-global-path": global, "task-list-directory": filepath.Join(config, "tasks", o.Arguments.SessionID)} {
		if err := os.WriteFile(filepath.Join(home, name), []byte(path), 0600); err != nil {
			t.Fatal(err)
		}
	}
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	env := []string{}
	for name, value := range o.Environment {
		env = append(env, name+"="+value)
	}
	env = append(env, "TERM=xterm-256color", "COLORTERM=truecolor")
	code, err := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, env)
	if _, probeErr := nativeTUIPresentationTerminalProbe(os.Stdin.Fd()); probeErr != nil {
		t.Fatal(probeErr)
	}
	after, stateErr := term.GetState(os.Stdin.Fd())
	if err != nil || code != 0 || stateErr != nil || !reflect.DeepEqual(before, after) || calls.Load() != 7 {
		t.Fatalf("Actual task-list exit code%d err%v terminal%v calls%d", code, err, stateErr, calls.Load())
	}
	receipt := map[string]any{"code": code, "requests": calls.Load(), "terminalRestored": true}
	data, _ := json.Marshal(receipt)
	if err = os.WriteFile(filepath.Join(home, "task-list-receipt.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	wait := time.NewTimer(3 * time.Second)
	defer wait.Stop()
	for {
		if _, err = os.Stat(filepath.Join(home, "task-list-restoration-inspected")); err == nil {
			return
		}
		select {
		case <-wait.C:
			t.Fatal("Driver did not inspect actual live termios")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
