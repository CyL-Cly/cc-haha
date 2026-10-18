package application

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
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

func TestActualCLIThinkingPTYSelectionMidConversationCancelConfirmAndRestore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY; separate ConPTY evidence required")
	}
	python, e := exec.LookPath("python3")
	if e != nil {
		t.Skip("native Python PTY required")
	}
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	home := t.TempDir()
	_ = os.WriteFile(filepath.Join(home, "disposable-tui-thinking"), []byte("private"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_thinking_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_TUI_THINKING_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("actual thinking PTY %v %.6500s", e, out)
	}
	if !strings.Contains(string(out), `"terminalRestored": true`) {
		t.Fatal("terminal restoration proof absent", string(out))
	}
}
func TestNativeTUIThinkingFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_TUI_THINKING_HELPER") != "1" {
		t.Skip("owned native PTY helper")
	}
	home := os.Getenv("HOME")
	if _, e := os.Stat(filepath.Join(home, "disposable-tui-thinking")); e != nil {
		t.Fatal("private marker required")
	}
	var count atomic.Int32
	var configOK atomic.Bool
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		n := count.Add(1)
		request := obj(body)
		thinking, _ := request.Object("thinking")
		if n == 1 {
			// Source's provider policy maps the session's adaptive choice to
			// enabled for Sonnet 4.6, which lacks adaptive-thinking support.
			if thinking.String("type") != "enabled" || string(thinking.Get("budget_tokens")) != "31999" {
				t.Errorf("first current thinking config %s", request.Get("thinking"))
			}
			response(w, "thinking-first", "", nil, "TUI_THINKING_FIRST_DONE")
		} else if n == 2 {
			configOK.Store(request.Get("thinking") == nil || thinking.String("type") == "disabled")
			response(w, "thinking-second", "", nil, "TUI_THINKING_SECOND_DONE")
		} else {
			t.Error("picker queried model", n)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Arguments.Model = "claude-sonnet-4-6"
	o.Arguments.Thinking = requestcontext.Thinking{Type: "disabled"}
	o.Stderr = os.Stderr
	o.OnSDK = func(data json.RawMessage) error {
		if obj(data).String("type") == "result" && count.Load() == 2 {
			return os.WriteFile(filepath.Join(home, "thinking-second-finalized.json"), data, 0600)
		}
		return nil
	}
	before, e := term.GetState(os.Stdin.Fd())
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	code, e := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, os.Environ())
	if e != nil || code != 0 {
		t.Fatal(code, e)
	}
	_, e = nativeTUIPresentationTerminalProbe(os.Stdin.Fd())
	if e != nil {
		t.Fatal(e)
	}
	after, e := term.GetState(os.Stdin.Fd())
	if e != nil {
		t.Fatal(e)
	}
	receipt, _ := json.Marshal(map[string]any{"requests": count.Load(), "actualDisabledSecondRequest": configOK.Load(), "terminalRestored": reflect.DeepEqual(before, after), "before": fmt.Sprintf("%#v", before), "after": fmt.Sprintf("%#v", after)})
	if e = os.WriteFile(filepath.Join(home, "thinking-receipt.json"), receipt, 0600); e != nil {
		t.Fatal(e)
	}
}
