package application

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualRunCommandExternalPromptEditorSelectionAndLifecyclePTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix physical PTY; Windows native machine remains required")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"visual", "editor", "error", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(filepath.Join(home, "owned-prompt-editor-marker"), []byte("owned"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, python, "testdata/terminal_prompt_editor_pty.py", binary, home, scenario)
			child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=" + os.Getenv("PATH"), "TERM=xterm-256color", "CC_HAHA_PUBLIC_PROMPT_EDITOR_HELPER=1"}
			capturePublicGoCoverage(t, child)
			output, err := child.CombinedOutput()
			if err != nil {
				t.Fatalf("actual public RunCommand prompt editor %v: %s", err, output)
			}
			if !strings.Contains(string(output), `"qualified": true`) {
				t.Fatalf("missing actual editor evidence: %s", output)
			}
		})
	}
}

func TestPublicRunCommandPromptEditorFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_PUBLIC_PROMPT_EDITOR_HELPER") != "1" {
		t.Skip("owned public RunCommand PTY helper")
	}
	home := os.Getenv("HOME")
	if data, err := os.ReadFile(filepath.Join(home, "owned-prompt-editor-marker")); err != nil || string(data) != "owned" {
		t.Fatal("private fixture marker", err)
	}
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(204)
			return
		}
		requests.Add(1)
		w.WriteHeader(503)
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Arguments.Tools = nil
	o.Arguments.SystemPrompt = ""
	o.Arguments.PermissionMode = "default"
	o.Stderr = os.Stderr
	for _, key := range []string{"EDITOR", "VISUAL", "PATH", "TMPDIR", "CC_HAHA_EDITOR_EXPECTED_HOME"} {
		o.Environment[key] = os.Getenv(key)
		o.Native.Environment[key] = os.Getenv(key)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stopped:
				return
			case <-ctx.Done():
				return
			case <-tick.C:
				if _, err := os.Stat(filepath.Join(home, "cancel-editor")); err == nil {
					cancel()
					return
				}
			}
		}
	}()
	code, runError := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, os.Environ())
	result := map[string]any{"code": code, "error": fmt.Sprint(runError), "contextCanceled": ctx.Err() == context.Canceled, "modelRequests": requests.Load()}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "runcommand-result.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 {
		t.Fatal("prompt editing caused a model request", requests.Load())
	}
	if ctx.Err() != context.Canceled && (runError != nil || code != 0) {
		t.Fatal("public RunCommand result", code, runError)
	}
}
