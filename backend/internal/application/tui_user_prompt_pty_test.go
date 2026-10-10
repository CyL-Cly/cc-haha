package application

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
)

func TestActualCLIUserPromptBlockingWarningAndNextHumanPhysicalToolsPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY; ConPTY separate required")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("native Python PTY required")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err = os.WriteFile(filepath.Join(home, "disposable-user-prompt"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 33*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_user_prompt_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_TUI_USER_PROMPT_HELPER=1"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual user prompt PTY %v %.6500s", err, out)
	}
	if !strings.Contains(string(out), `"terminalRestored": true`) {
		t.Fatal("native terminal restoration proof absent", string(out))
	}
}

func TestNativeTUIUserPromptFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_TUI_USER_PROMPT_HELPER") != "1" {
		t.Skip("owned private PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-user-prompt")); err != nil {
		t.Fatal(err)
	}
	var count atomic.Int32
	var firstBlockedZeroPOST atomic.Bool
	var target, journal string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		index := count.Add(1)
		if strings.Contains(string(body), "PRIVATE_FIRST_BLOCKED_PROMPT") || strings.Contains(string(body), "UserPromptSubmit operation blocked") {
			t.Error("erased input/warning reached next-human API")
		}
		if index == 1 {
			data, err := os.ReadFile(journal)
			firstBlockedZeroPOST.Store(err == nil && len(strings.Split(strings.TrimSpace(string(data)), "\n")) == 2)
			response(w, "prompt-write", "Write", map[string]any{"file_path": target, "content": "actual-private-tools-after-prompt-block"}, "")
		} else if index == 2 {
			response(w, "prompt-read", "Read", map[string]any{"file_path": target}, "")
		} else {
			if !strings.Contains(string(body), "actual-private-tools-after-prompt-block") {
				t.Error("actual native Read output absent")
			}
			response(w, "prompt-done", "", nil, "ACTUAL_USER_PROMPT_NEXT_HUMAN_DONE")
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Stderr = os.Stderr
	target, journal = filepath.Join(o.WorkDir, "prompt-tools.txt"), filepath.Join(o.WorkDir, "user-prompt-hook-inputs.jsonl")
	script := filepath.Join(o.WorkDir, "user-prompt-hook.py")
	code := fmt.Sprintf("import os,json,sys\nx=json.load(sys.stdin)\nfirst=not os.path.exists(%q)\nwith open(%q,'a') as f:f.write(json.dumps(x)+'\\n')\nprint(json.dumps({'decision':'block','reason':'actual-private-prompt-block'} if first else {}))\n", journal, journal)
	if err := os.WriteFile(script, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(o.Environment["CLAUDE_CONFIG_DIR"], 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), raw(map[string]any{"hooks": map[string]any{"UserPromptSubmit": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/usr/bin/python3 " + strconv.Quote(script)}}}}}}), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 28*time.Second)
	defer cancel()
	exitCode, err := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, os.Environ())
	if err != nil || exitCode != 0 {
		t.Fatal(exitCode, err)
	}
	beforeNativeProbe, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = nativeTUIPresentationTerminalProbe(os.Stdin.Fd()); err != nil {
		t.Fatal(err)
	}
	after, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	actualFile, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	receipt := raw(map[string]any{"requests": count.Load(), "firstBlockedZeroPOST": firstBlockedZeroPOST.Load(), "physicalTools": string(actualFile) == "actual-private-tools-after-prompt-block", "terminalRestored": reflect.DeepEqual(before, after), "terminalBefore": fmt.Sprintf("%#v", before), "beforeNativeProbe": fmt.Sprintf("%#v", beforeNativeProbe), "terminalAfter": fmt.Sprintf("%#v", after), "hookInputs": len(strings.Split(strings.TrimSpace(string(inputs)), "\n"))})
	if err = os.WriteFile(filepath.Join(home, "user-prompt-receipt.json"), receipt, 0600); err != nil {
		t.Fatal(err)
	}
}
