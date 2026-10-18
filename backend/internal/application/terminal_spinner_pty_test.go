//go:build !windows

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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
)

func TestActualRunCommandSpinnerStreamTaskVerbPermissionNextHumanAndRestoredPTY(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err = os.WriteFile(filepath.Join(home, "owned-spinner-pty"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, "testdata/terminal_spinner_pty.py", binary, home)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "TMPDIR=" + home, "TERM=xterm-256color", "COLORTERM=truecolor", "CC_HAHA_SPINNER_PTY_HELPER=1"}
	capturePublicGoCoverage(t, command)
	output, err := command.CombinedOutput()
	if err != nil || !bytes.Contains(output, []byte(`"terminalRestored": true`)) {
		journal, _ := os.ReadFile(filepath.Join(home, "spinner-terminal.txt"))
		screen, _ := os.ReadFile(filepath.Join(home, "spinner-final-screen.txt"))
		stage, _ := os.ReadFile(filepath.Join(home, "spinner-driver-stage.txt"))
		t.Fatalf("actual public Spinner %v %s stage%s screen%s journal%s", err, output, stage, screen, journal[max(0, len(journal)-8000):])
	}
}

func TestNativeTerminalSpinnerPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_SPINNER_PTY_HELPER") != "1" {
		t.Skip("owned private Spinner PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "owned-spinner-pty")); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	var actualFile string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, err := io.ReadAll(request.Body)
		if err != nil || request.Header.Get("x-api-key") != "fake-only-loopback" {
			t.Error("actual private model transport", err, request.Header)
		}
		index := requests.Add(1)
		if err = os.WriteFile(filepath.Join(home, fmt.Sprintf("spinner-request-%d.json", index)), body, 0600); err != nil {
			t.Error(err)
		}
		switch index {
		case 1:
			if !bytes.Contains(body, []byte("PUBLIC_SPINNER_HUMAN_1")) {
				t.Error("first actual human missing", string(body))
			}
			response(w, "spinner-create", "TaskCreate", map[string]any{"subject": "PUBLIC_SPINNER_TASK", "activeForm": "PUBLIC_SPINNER_ACTIVE", "description": "actual durable public task"}, "")
		case 2:
			response(w, "spinner-update", "TaskUpdate", map[string]any{"taskId": "1", "status": "in_progress"}, "")
		case 3:
			w.Header().Set("Content-Type", "text/event-stream")
			emit := func(kind string, value any) {
				_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, raw(value))
				if err != nil {
					t.Error(err)
				}
				w.(http.Flusher).Flush()
			}
			wait := func(name string) bool {
				if err := os.WriteFile(filepath.Join(home, "spinner-stage-"+name), []byte("actual transport flushed"), 0600); err != nil {
					t.Error(err)
					return false
				}
				timer := time.NewTicker(10 * time.Millisecond)
				defer timer.Stop()
				for {
					if _, err := os.Stat(filepath.Join(home, "spinner-release-"+name)); err == nil {
						return true
					}
					select {
					case <-request.Context().Done():
						return false
					case <-timer.C:
					}
				}
			}
			emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "spinner-held-stream", "type": "message", "role": "assistant", "model": "fixture-model", "content": []any{}, "usage": map[string]any{"input_tokens": 12, "output_tokens": 0}}})
			emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "thinking", "thinking": ""}})
			emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "thinking_delta", "thinking": strings.Repeat("x", 400) + "😀"}})
			if !wait("thinking") {
				return
			}
			emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "signature_delta", "signature": "PRIVATE_SPINNER_SIGNATURE_NOT_A_RESPONSE_TOKEN"}})
			emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
			emit("content_block_start", map[string]any{"type": "content_block_start", "index": 1, "content_block": map[string]any{"type": "text", "text": ""}})
			emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 1, "delta": map[string]any{"type": "text_delta", "text": "PUBLIC_SPINNER_PARTIAL"}})
			if !wait("partial") {
				return
			}
			emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 1, "delta": map[string]any{"type": "text_delta", "text": "\nPUBLIC_SPINNER_NEXT_PARTIAL"}})
			if !wait("newline") {
				return
			}
			emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 1})
			emit("content_block_start", map[string]any{"type": "content_block_start", "index": 2, "content_block": map[string]any{"type": "tool_use", "id": "spinner-write", "name": "Write", "input": map[string]any{}}})
			emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 2, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(raw(map[string]any{"file_path": actualFile, "content": "PUBLIC_SPINNER_PHYSICAL_DATA\n"}))}})
			emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 2})
			emit("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "tool_use"}, "usage": map[string]any{"output_tokens": 130}})
			emit("message_stop", map[string]any{"type": "message_stop"})
		case 4:
			if !bytes.Contains(body, []byte("spinner-write")) || bytes.Contains(body, []byte(`"is_error":true`)) {
				t.Error("actual approval/Write result missing", string(body))
			}
			data, err := os.ReadFile(actualFile)
			if err != nil || string(data) != "PUBLIC_SPINNER_PHYSICAL_DATA\n" {
				t.Error("actual physical Write", err, string(data))
			}
			response(w, "spinner-work-complete", "", nil, "PUBLIC_SPINNER_WRITE_DONE")
		case 5:
			messages := string(obj(body).Get("messages"))
			if !strings.Contains(messages, "PUBLIC_SPINNER_NEXT_HUMAN") || !strings.Contains(messages, "PUBLIC_SPINNER_HUMAN_1") || !strings.Contains(messages, "spinner-write") || strings.Count(messages, "<command-name>/help</command-name>") != 1 || strings.Count(messages, "<local-command-stdout>Help dialog dismissed</local-command-stdout>") != 1 || strings.Contains(messages, "terminal_spinner") || strings.Contains(messages, "Claude understands your codebase") {
				t.Error("same Harness canonical next human", messages)
			}
			response(w, "spinner-next-read", "Read", map[string]any{"file_path": actualFile}, "")
		case 6:
			if !bytes.Contains(body, []byte("spinner-next-read")) || !bytes.Contains(body, []byte("PUBLIC_SPINNER_PHYSICAL_DATA")) || !bytes.Contains(body, []byte("PUBLIC_SPINNER_NEXT_HUMAN")) || bytes.Contains(body, []byte(`"is_error":true`)) {
				t.Error("actual next-human Read result missing", string(body))
			}
			response(w, "spinner-next-complete", "", nil, "PUBLIC_SPINNER_NEXT_DONE")
		default:
			t.Error("extra actual model request", index, string(body))
			response(w, "unexpected-spinner", "", nil, "unexpected")
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Stderr = os.Stderr
	actualFile = filepath.Join(o.WorkDir, "spinner-physical.txt")
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, ".claude.json"), []byte(`{"hasCompletedOnboarding":true,"theme":"dark","verbose":true,"spinnerFuture":{"keep":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"prefersReducedMotion":false,"spinnerTipsEnabled":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	parsed, result, err := ParseCommand([]string{"--model", "fixture-model", "--session-id", o.Arguments.SessionID, "--tools", "TaskCreate,TaskUpdate,Write,Read", "--permission-mode", "default"}, o.Version, o.WorkDir, home, o.Environment)
	if err != nil || result.Parsed == nil {
		t.Fatal("actual public parse", err, result)
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
	code, err := RunCommand(ctx, parsed, o, os.Stdin, os.Stdout, environment)
	if _, probeErr := nativeTUIPresentationTerminalProbe(os.Stdin.Fd()); probeErr != nil {
		t.Fatal(probeErr)
	}
	after, stateErr := term.GetState(os.Stdin.Fd())
	if err != nil || code != 0 || stateErr != nil || !reflect.DeepEqual(before, after) || requests.Load() != 6 {
		t.Fatalf("actual Spinner exit code%d err%v terminal%v HTTP%d", code, err, stateErr, requests.Load())
	}
	data, _ := json.Marshal(map[string]any{"code": code, "requests": requests.Load(), "terminalRestored": true})
	if err = os.WriteFile(filepath.Join(home, "spinner-pty-receipt.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		if _, err = os.Stat(filepath.Join(home, "spinner-restoration-inspected")); err == nil {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("driver did not inspect live termios")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
