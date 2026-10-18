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

func TestActualCLIWorkflowTreePauseSaveDynamicCommandAndNextRoundPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY; ConPTY separate")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("native PTY unavailable")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	_ = os.WriteFile(filepath.Join(home, "disposable-workflows-pty"), []byte("owned"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_workflows_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_WORKFLOWS_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual workflow PTY %v %.7000s", err, output)
	}
	if !strings.Contains(string(output), `"terminalRestored": true`) {
		t.Fatal(string(output))
	}
}
func TestNativeCLIWorkflowPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_WORKFLOWS_PTY_HELPER") != "1" {
		t.Skip("owned PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-workflows-pty")); err != nil {
		t.Fatal(err)
	}
	var parent, child, canceled atomic.Int32
	script := `export const meta={name:'pty-saved-workflow',description:'PUBLIC_PTY_WORKFLOW_RUN'};return await agent('PRIVATE_PTY_WORKFLOW_AGENT_PROMPT')`
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "You are a subagent spawned by a workflow orchestration script") {
			child.Add(1)
			<-r.Context().Done()
			canceled.Add(1)
			return
		}
		n := parent.Add(1)
		switch n {
		case 1:
			response(w, "wf-pty-launch", "Workflow", map[string]any{"script": script}, "")
		case 2:
			response(w, "wf-pty-started", "", nil, "WORKFLOW_PUBLIC_READY_564")
		case 3:
			if !strings.Contains(string(body), "Run the saved workflow") {
				t.Error("public saved command was not consumed", string(body))
			}
			response(w, "wf-pty-next", "", nil, "WORKFLOW_SAVED_COMMAND_DONE_564")
		default:
			t.Error("unexpected provider request", n, string(body))
			response(w, "unexpected", "", nil, "unexpected")
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Stderr = os.Stderr
	o.Arguments.Tools = []string{"Workflow,Read,Write"}
	o.Environment["CLAUDE_CODE_TMPDIR"] = filepath.Join(o.Environment["HOME"], "tmp")
	o.Native.Environment["CLAUDE_CODE_TMPDIR"] = o.Environment["CLAUDE_CODE_TMPDIR"]
	_ = os.MkdirAll(o.Environment["CLAUDE_CODE_TMPDIR"], 0700)
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.MkdirAll(config, 0700)
	_ = os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}), 0600)
	_ = os.WriteFile(filepath.Join(home, "workflows-workdir.txt"), []byte(o.WorkDir), 0600)
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
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
	receipt := map[string]any{"terminalRestored": reflect.DeepEqual(before, after), "parentRequests": parent.Load(), "childRequests": child.Load(), "canceledRequests": canceled.Load(), "exitCode": code}
	data, _ := json.Marshal(receipt)
	_ = os.WriteFile(filepath.Join(home, "workflows-receipt.json"), data, 0600)
}
