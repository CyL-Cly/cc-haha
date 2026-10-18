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

func TestActualCLIIdleWorkflowNotificationArtifactAndDraftPreservationPTY(t *testing.T) {
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
	_ = os.WriteFile(filepath.Join(home, "disposable-workflow-completion-pty"), []byte("owned"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_workflow_completion_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_WORKFLOW_COMPLETION_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual idle workflow PTY %v %.7000s", err, output)
	}
	if !strings.Contains(string(output), `"terminalRestored": true`) {
		t.Fatal(string(output))
	}
}
func TestNativeCLIIdleWorkflowCompletionPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_WORKFLOW_COMPLETION_PTY_HELPER") != "1" {
		t.Skip("owned PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-workflow-completion-pty")); err != nil {
		t.Fatal(err)
	}
	var parent, child atomic.Int32
	var childArtifact, notificationArtifact string
	release := filepath.Join(home, "release-child")
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "You are a subagent spawned by a workflow orchestration script") {
			if child.Add(1) == 1 {
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					if _, err := os.Stat(release); err == nil {
						break
					}
					select {
					case <-r.Context().Done():
						return
					case <-ticker.C:
					}
				}
				response(w, "wf-child-write", "Write", map[string]any{"file_path": childArtifact, "content": "PHYSICAL_PTY_WORKFLOW_CHILD_ARTIFACT"}, "")
			} else {
				response(w, "wf-child-final", "", nil, "PHYSICAL_PTY_WORKFLOW_CHILD_RESULT")
			}
			return
		}
		switch parent.Add(1) {
		case 1:
			response(w, "wf-completion-launch", "Workflow", map[string]any{"script": `export const meta={name:'idle-wake-proof',description:'Public idle workflow completion'};return await agent('Create the actual child artifact and return the result.')`}, "")
		case 2:
			response(w, "wf-completion-ready", "", nil, "WORKFLOW_IDLE_READY_708")
		case 3:
			if !strings.Contains(string(body), "task-notification") || !strings.Contains(string(body), "PHYSICAL_PTY_WORKFLOW_CHILD_RESULT") {
				t.Error("actual task notification missing", string(body))
			}
			response(w, "wf-completion-artifact", "Write", map[string]any{"file_path": notificationArtifact, "content": "PHYSICAL_PTY_NOTIFICATION_ARTIFACT"}, "")
		case 4:
			response(w, "wf-completion-parent", "", nil, "WORKFLOW_IDLE_NOTIFY_DONE_708")
		case 5:
			if !strings.Contains(string(body), "PRIVATE_DRAFT_RETAINED_708") {
				t.Error("idle followup lost editable human draft", string(body))
			}
			response(w, "wf-completion-human", "", nil, "WORKFLOW_DRAFT_NEXT_DONE_708")
		default:
			t.Error("unexpected request", parent.Load())
			response(w, "unexpected", "", nil, "unexpected")
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Stderr = os.Stderr
	o.Arguments.Tools = []string{"Workflow,Read,Write"}
	childArtifact = filepath.Join(o.WorkDir, "child-artifact.txt")
	notificationArtifact = filepath.Join(o.WorkDir, "notification-artifact.txt")
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["CLAUDE_CODE_TMPDIR"] = filepath.Join(o.Environment["HOME"], "tmp")
	}
	_ = os.MkdirAll(o.Environment["CLAUDE_CODE_TMPDIR"], 0700)
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.MkdirAll(config, 0700)
	_ = os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}), 0600)
	_ = os.WriteFile(filepath.Join(home, "completion-workdir.txt"), []byte(o.WorkDir), 0600)
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 38*time.Second)
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
	receipt := map[string]any{"terminalRestored": reflect.DeepEqual(before, after), "parentRequests": parent.Load(), "childRequests": child.Load(), "exitCode": code}
	data, _ := json.Marshal(receipt)
	_ = os.WriteFile(filepath.Join(home, "completion-receipt.json"), data, 0600)
	for {
		if _, err := os.Stat(filepath.Join(home, "completion-pty-release")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("PTY restoration inspector did not release fixture", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if parent.Load() != 5 || child.Load() != 2 || !reflect.DeepEqual(before, after) {
		t.Fatal(string(data))
	}
}
