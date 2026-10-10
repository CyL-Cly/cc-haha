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

func TestActualCLIGoalMenuEvaluatorContinuationCompletionAndExitPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY; Windows ConPTY separate")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("native PTY driver unavailable")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err = os.WriteFile(filepath.Join(home, "disposable-goal-pty"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_goal_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_TUI_GOAL_HELPER=1"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Goal PTY %v %.7500s", err, out)
	}
	if !strings.Contains(string(out), `"terminalRestored": true`) {
		t.Fatal(string(out))
	}
}

func TestNativeTUIGoalFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_TUI_GOAL_HELPER") != "1" {
		t.Skip("owned private PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-goal-pty")); err != nil {
		t.Fatal(err)
	}
	var posts atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := posts.Add(1)
		switch n {
		case 1:
			response(w, "goal-first", "", nil, "FIRST_GOAL_MAIN_637")
		case 2:
			if !strings.Contains(string(body), "You are evaluating a hook") {
				t.Error("not actual evaluator")
			}
			response(w, "goal-eval-one", "", nil, `{"ok":false,"reason":"verify PRIVATE_TUI_GOAL_637"}`)
		case 3:
			response(w, "goal-second", "", nil, "SECOND_GOAL_MAIN_DONE_637")
		case 4:
			response(w, "goal-eval-two", "", nil, `{"ok":true}`)
		case 5:
			response(w, "goal-next", "", nil, "GOAL_NORMAL_NEXT_637")
		default:
			t.Error("unexpected HTTP calls", n)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Stderr = os.Stderr
	o.Environment["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = "fixture-evaluator"
	if err := os.MkdirAll(o.Environment["CLAUDE_CONFIG_DIR"], 0700); err != nil {
		t.Fatal(err)
	}
	canonicalWork, err := filepath.EvalSymlinks(o.WorkDir)
	if err != nil {
		t.Fatal(err)
	}
	global, _ := json.Marshal(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{canonicalWork: map[string]any{"hasTrustDialogAccepted": true}, o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}})
	if err := os.WriteFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], ".claude.json"), global, 0600); err != nil {
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
	files, _ := filepath.Glob(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects", "*", "*.jsonl"))
	canonical := false
	for _, file := range files {
		data, _ := os.ReadFile(file)
		canonical = canonical || strings.Contains(string(data), "Goal marked complete.") && strings.Contains(string(data), "GOAL_NORMAL_NEXT_637") && !strings.Contains(string(data), "You are evaluating a hook")
	}
	receipt, _ := json.Marshal(map[string]any{"requests": posts.Load(), "canonical": canonical, "terminalRestored": reflect.DeepEqual(before, after)})
	if err = os.WriteFile(filepath.Join(home, "goal-receipt.json"), receipt, 0600); err != nil {
		t.Fatal(err)
	}
}
