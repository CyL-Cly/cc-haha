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

func TestActualCLIInsightsMenuReportAndSameHarnessRestorePTY(t *testing.T) {
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
	if err = os.WriteFile(filepath.Join(home, "disposable-insights-pty"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_insights_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_TUI_INSIGHTS_HELPER=1"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Insights PTY %v %.7500s", err, out)
	}
	if !strings.Contains(string(out), `"terminalRestored": true`) {
		t.Fatal(string(out))
	}
}

func TestNativeTUIInsightsFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_TUI_INSIGHTS_HELPER") != "1" {
		t.Skip("owned private PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-insights-pty")); err != nil {
		t.Fatal(err)
	}
	var posts atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := posts.Add(1)
		text := `{}`
		if strings.Contains(string(body), "You're writing an") {
			text = `{"whats_working":"PRIVATE_INSIGHTS_PTY_REPORT_722"}`
		}
		if strings.Contains(string(body), "The user just ran /insights") {
			text = "PRIVATE_INSIGHTS_PTY_REPORT_READY_722"
		}
		if n == 10 {
			text = "PRIVATE_INSIGHTS_PTY_NEXT_722"
		}
		response(w, "insights-response", "", nil, text)
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Stderr = os.Stderr
	location, _ := json.Marshal(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "usage-data", "report.html"))
	if err := os.WriteFile(filepath.Join(home, "insights-report-location.json"), location, 0600); err != nil {
		t.Fatal(err)
	}
	o.Environment["ANTHROPIC_DEFAULT_OPUS_MODEL"] = "fixture-insights"
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
		canonical = canonical || strings.Contains(string(data), "PRIVATE_INSIGHTS_PTY_REPORT_READY_722") && strings.Contains(string(data), "PRIVATE_INSIGHTS_PTY_NEXT_722") && !strings.Contains(string(data), "You're writing an")
	}
	report, reportError := os.ReadFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "usage-data", "report.html"))
	if reportError != nil || !strings.Contains(string(report), "PRIVATE_INSIGHTS_PTY_REPORT_722") {
		t.Fatal("actual report", reportError)
	}
	receipt, _ := json.Marshal(map[string]any{"requests": posts.Load(), "canonical": canonical, "terminalRestored": reflect.DeepEqual(before, after)})
	if err = os.WriteFile(filepath.Join(home, "insights-receipt.json"), receipt, 0600); err != nil {
		t.Fatal(err)
	}
}
