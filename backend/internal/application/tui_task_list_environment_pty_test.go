package application

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
)

func TestActualRunCommandTaskListSettingsEnvironmentSwitchNextToolCanonicalHumanAndTermiosPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY; ConPTY separate")
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
	if err = os.WriteFile(filepath.Join(home, "disposable-task-env-pty"), []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, "testdata/tui_task_list_environment_pty.py", binary, home)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "TMPDIR=" + home, "TERM=xterm-256color", "COLORTERM=truecolor", "CC_HAHA_TUI_TASK_ENV_HELPER=1"}
	capturePublicGoCoverage(t, command)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("Actual RunCommand task-list settings/environment/watcher", err, string(output))
	}
	t.Log(string(output))
}

func TestNativeTaskListEnvironmentPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_TUI_TASK_ENV_HELPER") != "1" {
		t.Skip("Owned disposable PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-task-env-pty")); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(request.Body)
		number := calls.Add(1)
		if bytes.Contains(body, []byte(`"is_error":true`)) {
			t.Error("Actual task tool failed", string(body))
		}
		switch number {
		case 1:
			response(w, "task-env-before", "TaskCreate", map[string]any{"subject": "ORIGINAL_LIST_TASK", "description": "Real task before setting env"}, "")
		case 2:
			response(w, "task-env-done-before", "", nil, "PUBLIC_TASK_ENV_DONE_1")
		case 3:
			if !bytes.Contains(body, []byte("TASK_ENV_HUMAN_2")) || !bytes.Contains(body, []byte("task-env-before")) {
				t.Error("Settings environment changed the actual previous history", string(body))
			}
			response(w, "task-env-after", "TaskCreate", map[string]any{"subject": "NEXT_LIST_TASK", "description": "Real task after setting env"}, "")
		case 4:
			response(w, "task-env-done-after", "", nil, "PUBLIC_TASK_ENV_DONE_2")
		case 5:
			messages := obj(body).Get("messages")
			for _, value := range []string{"TASK_ENV_HUMAN_1", "TASK_ENV_HUMAN_2", "TASK_ENV_HUMAN_3", "task-env-before", "task-env-after"} {
				if !bytes.Contains(messages, []byte(value)) {
					t.Error("Next same-Harness human lost the actual task history", value, string(body))
				}
			}
			if bytes.Count(messages, []byte("<command-name>/help</command-name>")) != 1 || bytes.Count(messages, []byte("<local-command-stdout>Help dialog dismissed</local-command-stdout>")) != 1 {
				t.Error("Ordinary Help changed the Source canonical completion", string(body))
			}
			response(w, "task-env-human-done", "", nil, "PUBLIC_TASK_ENV_DONE_3")
		default:
			t.Error("Unexpected real model request", number, string(body))
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive, o.ForegroundShellProgress = true, true
	o.Arguments.Tools = []string{"TaskCreate"}
	o.Stderr = os.Stderr
	for _, environment := range []map[string]string{o.Environment, o.Native.Environment} {
		environment["CLAUDE_CODE_TASK_LIST_ID"] = "actual-original-list"
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(config, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"futureTaskSettings":{"keep":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(config, ".claude.json")
	if err := os.WriteFile(global, raw(map[string]any{"hasCompletedOnboarding": true, "showExpandedTodos": false, "showSpinnerTree": false, "taskListFuture": map[string]bool{"keep": true}, "projects": map[string]any{o.WorkDir: map[string]bool{"hasTrustDialogAccepted": true}}}), 0600); err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{"settings": settingsPath, "global": global, "original": filepath.Join(config, "tasks", "actual-original-list"), "next": filepath.Join(config, "tasks", "actual-next-list")}
	if err := os.WriteFile(filepath.Join(home, "task-env-paths.json"), raw(paths), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	environment := []string{}
	for name, value := range o.Environment {
		environment = append(environment, name+"="+value)
	}
	environment = append(environment, "TERM=xterm-256color", "COLORTERM=truecolor")
	code, runErr := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, environment)
	_, probeErr := nativeTUIPresentationTerminalProbe(os.Stdin.Fd())
	after, stateErr := term.GetState(os.Stdin.Fd())
	if runErr != nil || code != 0 || calls.Load() != 5 || probeErr != nil || stateErr != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("Actual dynamic task-list lifecycle/terminal restoration", code, runErr, calls.Load(), probeErr, stateErr)
	}
	if err := os.WriteFile(filepath.Join(home, "task-env-receipt.json"), raw(map[string]any{"exitCode": code, "requests": calls.Load(), "terminalRestored": true}), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		if _, err := os.Stat(filepath.Join(home, "task-env-restoration-inspected")); err == nil {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("Driver did not inspect the same live terminal after cleanup")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
