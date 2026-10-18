package application

import (
	"bufio"
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
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
)

func TestActualCLIPlanOriginalEditorHandoffReadAndNextHumanPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY; Windows native machine required")
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
	if err = os.WriteFile(filepath.Join(home, "private-plan-pty"), []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, "testdata/terminal_plan_pty.py", binary, home)
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=" + os.Getenv("PATH"), "TERM=xterm-256color", "CC_HAHA_PLAN_PTY_HELPER=1"}
	capturePublicGoCoverage(t, command)
	data, err := command.CombinedOutput()
	if err != nil {
		system, _ := os.ReadFile(filepath.Join(home, "plan-request-system.json"))
		t.Fatalf("actual public plan/editor PTY %v %.6500s system %.2500s", err, data, system)
	}
	if !strings.Contains(string(data), `"terminalRestored": true`) {
		t.Fatalf("actual PTY receipts missing %.3000s", data)
	}
}

func TestNativePlanPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_PLAN_PTY_HELPER") != "1" {
		t.Skip("owned private CLI PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "private-plan-pty")); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	count := 0
	path := ""
	readEdited := false
	canonicalPlan := false
	var privateConfig string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		count++
		switch count {
		case 1:
			var input struct {
				System   []struct{ Text string }
				Messages []struct{ Content json.RawMessage }
			}
			if json.Unmarshal(body, &input) != nil {
				t.Error("invalid actual request")
			}
			_ = os.WriteFile(filepath.Join(home, "plan-request-system.json"), raw(input.System), 0600)
			pattern := regexp.MustCompile(regexp.QuoteMeta(filepath.Join(privateConfig, "plans")) + `/[^\s"\x60]+\.md`)
			blocks := append([]struct{ Text string }{}, input.System...)
			for _, message := range input.Messages {
				var text string
				if json.Unmarshal(message.Content, &text) == nil {
					blocks = append(blocks, struct{ Text string }{text})
				} else {
					var content []struct{ Text string }
					_ = json.Unmarshal(message.Content, &content)
					blocks = append(blocks, content...)
				}
			}
			for _, block := range blocks {
				if candidate := pattern.FindString(block.Text); candidate != "" {
					path = candidate
					break
				}
			}
			if path == "" {
				t.Error("actual plan attachment path missing")
				http.Error(w, "missing private plan path", 500)
				return
			}
			response(w, "plan-pty-write", "Write", map[string]any{"file_path": path, "content": "INITIAL_PLAN_FROM_REAL_WRITE"}, "")
		case 2:
			response(w, "plan-created", "", nil, "PLAN_FILE_CREATED_89EF")
		case 3:
			canonicalPlan = strings.Contains(string(body), "Enabled plan mode") && strings.Contains(string(body), "Opened plan in editor") && strings.Contains(string(body), "EDITED_PLAN_FROM_EXTERNAL_EDITOR")
			response(w, "plan-pty-read", "Read", map[string]any{"file_path": path}, "")
		case 4:
			readEdited = strings.Contains(string(body), "EDITED_PLAN_FROM_EXTERNAL_EDITOR")
			response(w, "plan-next", "", nil, "PLAN_NEXT_HUMAN_ACTUAL_READ_89EF")
		default:
			t.Error("extra model", count)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Stderr = os.Stderr
	o.Arguments.PermissionMode = "default"
	seedTerminalReleaseWarmFixture(t, o)
	privateConfig = o.Native.Environment["CLAUDE_CONFIG_DIR"]
	binary, _ := os.Executable()
	editor := filepath.Join(o.Environment["HOME"], "owned-editor")
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(binary, "'", "'\"'\"'") + "' -test.run=^TestNativePlanEditorPhysicalChild$ -test.count=1 -- \"$@\"\n"
	if err = os.WriteFile(editor, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"EDITOR": editor, "VISUAL": "", "NO_PROXY": "127.0.0.1,localhost", "CC_HAHA_PLAN_EDITOR_CHILD": "1", "CC_HAHA_PLAN_EDITOR_STATE": fmt.Sprintf("%+v", before), "CC_HAHA_PLAN_EDITOR_RECEIPT": filepath.Join(home, "editor-receipt.json")} {
		o.Environment[key] = value
		o.Native.Environment[key] = value
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	code, err := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, os.Environ())
	if err != nil || code != 0 {
		t.Fatal("actual public CLI result", code, err)
	}
	after, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "EDITED_PLAN_FROM_EXTERNAL_EDITOR\n" {
		t.Fatal("original plan file edit was not retained", err, string(data))
	}
	mu.Lock()
	receipt := map[string]any{"requests": count, "readEdited": readEdited, "canonicalPlan": canonicalPlan, "terminalRestored": reflect.DeepEqual(before, after)}
	mu.Unlock()
	if err = os.WriteFile(filepath.Join(home, "plan-pty-receipt.json"), raw(receipt), 0600); err != nil {
		t.Fatal(err)
	}
	// macOS detaches the controlling slave after this session leader exits.
	// Keep it alive until the outer driver checks the actual terminal flags.
	for {
		if _, err = os.Stat(filepath.Join(home, "plan-termios-release")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("physical terminal restoration not released")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestNativePlanEditorPhysicalChild(t *testing.T) {
	if os.Getenv("CC_HAHA_PLAN_EDITOR_CHILD") != "1" {
		t.Skip("owned editor child")
	}
	path := os.Args[len(os.Args)-1]
	home := os.Getenv("HOME")
	if !strings.HasPrefix(path, filepath.Join(home, ".claude", "plans")+string(filepath.Separator)) {
		t.Fatal("editor received another file", path)
	}
	state, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%+v", state) != os.Getenv("CC_HAHA_PLAN_EDITOR_STATE") {
		t.Fatal("terminal was not released to original editor", state)
	}
	fmt.Println("PLAN_EDITOR_READING_ACTUAL_TTY_89EF")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "save-plan" {
		t.Fatal("editor lost actual terminal input", err, line)
	}
	if err = os.WriteFile(path, []byte("EDITED_PLAN_FROM_EXTERNAL_EDITOR\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(os.Getenv("CC_HAHA_PLAN_EDITOR_RECEIPT"), raw(map[string]any{"actualOriginalFile": path, "terminalReleased": true, "stdin": "save-plan"}), 0600); err != nil {
		t.Fatal(err)
	}
}
