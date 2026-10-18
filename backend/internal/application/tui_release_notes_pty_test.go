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

func TestActualCLIReleaseNotesMenuCachedOutputAndNextHumanPTY(t *testing.T) {
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
	if err = os.WriteFile(filepath.Join(home, "disposable-release-pty"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_release_notes_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_TUI_RELEASE_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual release PTY %v %.7000s", err, out)
	}
	if !strings.Contains(string(out), `"terminalRestored": true`) {
		t.Fatal(string(out))
	}
}
func TestNativeTUIReleaseNotesFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_TUI_RELEASE_HELPER") != "1" {
		t.Skip("owned private PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-release-pty")); err != nil {
		t.Fatal(err)
	}
	var posts atomic.Int32
	var history atomic.Bool
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		posts.Add(1)
		history.Store(strings.Contains(string(body), "PRIVATE_TUI_RELEASE_ONE_6AF") && strings.Contains(string(body), "<local-command-stdout>"))
		response(w, "release-next", "", nil, "RELEASE_REAL_NEXT_HUMAN_6AF")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Stderr = os.Stderr
	cache := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "cache", "changelog.md")
	if err := os.MkdirAll(filepath.Dir(cache), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cache, []byte("## 2.0.0\n- PRIVATE_TUI_RELEASE_TWO_6AF\n## 1.0.0\n- PRIVATE_TUI_RELEASE_ONE_6AF\n"), 0600); err != nil {
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
	paths, _ := filepath.Glob(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects", "*", "*.jsonl"))
	canonical := false
	for _, path := range paths {
		data, _ := os.ReadFile(path)
		canonical = canonical || strings.Contains(string(data), "RELEASE_REAL_NEXT_HUMAN_6AF") && strings.Contains(string(data), "<command-name>/release-notes")
	}
	receipt, _ := json.Marshal(map[string]any{"requests": posts.Load(), "actualLocalHistory": history.Load(), "canonical": canonical, "terminalRestored": reflect.DeepEqual(before, after)})
	if err = os.WriteFile(filepath.Join(home, "release-receipt.json"), receipt, 0600); err != nil {
		t.Fatal(err)
	}
}
