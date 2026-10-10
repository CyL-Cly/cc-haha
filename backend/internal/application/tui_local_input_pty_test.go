package application

import (
	"context"
	"encoding/json"
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

func TestActualCLILocalNoQueryUsageAndModelOnlyMessagesPTY(t *testing.T) {
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
	if err = os.WriteFile(filepath.Join(home, "disposable-local-noquery"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_local_input_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_TUI_LOCAL_NOQUERY_HELPER=1"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual local noquery PTY %v %.6500s", err, out)
	}
	if !strings.Contains(string(out), `"terminalRestored": true`) {
		t.Fatal("native restoration proof absent", string(out))
	}
}

func TestNativeTUILocalNoQueryFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_TUI_LOCAL_NOQUERY_HELPER") != "1" {
		t.Skip("owned private PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-local-noquery")); err != nil {
		t.Fatal(err)
	}
	var count atomic.Int32
	var actualHistory atomic.Bool
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		count.Add(1)
		actualHistory.Store(strings.Contains(string(body), "Usage: /agent <agent> <prompt>") && strings.Contains(string(body), "This skill can only be invoked by Claude"))
		response(w, "local-users", "", nil, "LOCAL_VISIBLE_USERS_DONE_6CE1")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Stderr = os.Stderr
	path := filepath.Join(o.WorkDir, ".claude", "skills", "modelonly", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nuser-invocable: false\n---\nThis must not query a model directly."), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
	receipt, _ := json.Marshal(map[string]any{"requests": count.Load(), "actualLocalHistory": actualHistory.Load(), "terminalRestored": reflect.DeepEqual(before, after)})
	if err = os.WriteFile(filepath.Join(home, "local-noquery-receipt.json"), receipt, 0600); err != nil {
		t.Fatal(err)
	}
}
