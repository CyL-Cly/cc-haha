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

func TestActualCLILocalPanelsPermissionSaveAgentCRUDAndNextTurnPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY; ConPTY separate")
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
	if err = os.WriteFile(filepath.Join(home, "disposable-panels-pty"), []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_panels_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_TUI_PANELS_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		bodies, _ := os.ReadFile(filepath.Join(home, "panels-provider-messages.json"))
		if len(bodies) != 0 {
			if artifacts, artifactErr := filepath.Abs("../../../artifacts/go-migration-20261013"); artifactErr == nil {
				_ = os.WriteFile(filepath.Join(artifacts, "tui-panels-current-provider-messages.json"), bodies, 0600)
			}
		}
		journal, _ := os.ReadFile(filepath.Join(home, "panels-running-terminal.txt"))
		stage, _ := os.ReadFile(filepath.Join(home, "panels-running-stage.txt"))
		driverStack, _ := os.ReadFile(filepath.Join(home, "panels-driver-stack.txt"))
		if len(journal) > 12000 {
			journal = journal[len(journal)-12000:]
		}
		t.Fatalf("public panels PTY %v %.7500s stage%s driver%s terminal%s", err, out, stage, driverStack, journal)
	}
	if !strings.Contains(string(out), `"terminalRestored": true`) {
		t.Fatal(string(out))
	}
}
func TestNativeTUILocalPanelsFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_TUI_PANELS_HELPER") != "1" {
		t.Skip("owned private PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-panels-pty")); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		ordinal := calls.Add(1)
		request := obj(body)
		if err := os.WriteFile(filepath.Join(home, "panels-provider-messages.json"), request.Get("messages"), 0600); err != nil {
			t.Error("private provider message observation", err)
		}
		if ordinal == 1 && (strings.Contains(string(body), "PTY_SUPPRESSED_PROJECT_CLAUDE") || strings.Contains(string(body), "PTY_SUPPRESSED_PROJECT_AGENTS")) {
			t.Error("committed instruction mode did not reach actual request")
		}
		if ordinal == 3 && strings.Contains(string(request.Get("tools")), `"name":"Write"`) {
			t.Error("PTY saved deny missing from next request")
		}
		// Original normalizeMessagesForAPI converts canonical local_command
		// systems into user history on the next human ASK. The mounted panel's
		// interactive content and unrelated commands remain outside that history.
		modelMessages := string(request.Get("messages"))
		if strings.Count(modelMessages, "<command-name>/help</command-name>") != 1 || strings.Count(modelMessages, "<local-command-stdout>Help dialog dismissed</local-command-stdout>") != 1 || strings.Contains(modelMessages, "/permissions") || strings.Contains(modelMessages, "Claude understands your codebase") || strings.Contains(modelMessages, "Browse default commands:") {
			t.Error("local panel canonical history differs from actual Source projection", modelMessages)
		}
		if ordinal == 1 && !strings.Contains(modelMessages, "Start an actual background task.") {
			t.Error("first ASK was not the actual human after local panels", modelMessages)
		}
		switch ordinal {
		case 1:
			response(w, "panels-background", "Bash", map[string]any{"command": "printf PRIVATE_PTY_TASK_OUTPUT; sleep 30", "run_in_background": true, "description": "private PTY background task"}, "")
		case 2:
			response(w, "panels-background-done", "", nil, "PTY_BACKGROUND_STARTED_348")
		case 3:
			if !strings.Contains(string(body), "Continue after all local panels.") {
				t.Errorf("third request preceded normal prompt: %.2200s", request.Get("messages"))
			}
			response(w, "panels-next", "", nil, "PANELS_PUBLIC_NEXT_DONE_348")
		default:
			t.Errorf("unexpected model request %d: %.2200s", ordinal, request.Get("messages"))
			response(w, "unexpected", "", nil, "unexpected")
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Stderr = os.Stderr
	o.Native.Environment["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
	if err := os.MkdirAll(o.Environment["CLAUDE_CONFIG_DIR"], 0700); err != nil {
		t.Fatal(err)
	}
	work, err := filepath.EvalSymlinks(o.WorkDir)
	if err != nil {
		t.Fatal(err)
	}
	global := raw(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{work: map[string]any{"hasTrustDialogAccepted": true}, o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}})
	if err = os.WriteFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], ".claude.json"), global, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "panels-workdir.txt"), []byte(o.WorkDir), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "panels-configdir.txt"), []byte(o.Environment["CLAUDE_CONFIG_DIR"]), 0600); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"CLAUDE.md": "PTY_SUPPRESSED_PROJECT_CLAUDE", "AGENTS.md": "PTY_SUPPRESSED_PROJECT_AGENTS"} {
		if err = os.WriteFile(filepath.Join(o.WorkDir, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	code, err := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, os.Environ())
	if err != nil || code != 0 {
		t.Fatal(code, err)
	}
	if _, err = nativeTUIPresentationTerminalProbe(os.Stdin.Fd()); err != nil {
		t.Fatal(err)
	}
	after, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	receipt, _ := json.Marshal(map[string]any{"requests": calls.Load(), "terminalRestored": reflect.DeepEqual(before, after)})
	if err = os.WriteFile(filepath.Join(home, "panels-receipt.json"), receipt, 0600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(home, "panels-restoration-inspected")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("Physical slave restoration inspector did not release live leader", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
