package application

import (
	"context"
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

	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/charmbracelet/x/term"
)

func TestActualCLIThemePreviewSyntaxRollbackCommitAndNextPromptPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY; ConPTY separate")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python PTY unavailable")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(home, "disposable-theme-pty"), []byte("owned"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_theme_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "COLORTERM=truecolor", "TZ=UTC", "CC_HAHA_THEME_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("real public Theme PTY %v %.8500s", err, output)
	}
	if !strings.Contains(string(output), `"terminalRestored": true`) {
		t.Fatal(string(output))
	}
}

func TestNativeCLIThemePTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_THEME_PTY_HELPER") != "1" {
		t.Skip("owned public Theme PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-theme-pty")); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		index := requests.Add(1)
		_ = os.WriteFile(filepath.Join(home, "theme-request-"+strconv.Itoa(int(index))+".json"), body, 0600)
		want := "PRIVATE_THEME_DRAFT"
		if index == 2 {
			want = "NEXT_THEME_QUERY"
		}
		if !strings.Contains(string(body), want) || index > 2 {
			t.Errorf("sameHarness draft/nextinput missing %d %.1000s", index, body)
		}
		response(w, "theme-public-model", "", nil, "THEME_PUBLIC_DONE_"+strconv.Itoa(int(index)))
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	o.Stderr = os.Stderr
	o.Arguments.Tools = []string{""}
	o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrNotExist }
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.MkdirAll(config, 0700)
	global := filepath.Join(config, ".claude.json")
	user := filepath.Join(config, "settings.json")
	_ = os.WriteFile(global, raw(map[string]any{"theme": "dark", "editorMode": "normal", "autoUpdates": false, "futureGlobal": 17, "hasCompletedOnboarding": true, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}), 0600)
	_ = os.WriteFile(user, []byte(`{"futureUser":{"preserved":true}}`), 0600)
	_ = os.WriteFile(filepath.Join(home, "theme-paths.json"), raw(map[string]any{"global": global, "user": user}), 0600)
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
	_ = os.WriteFile(filepath.Join(home, "theme-receipt.json"), raw(map[string]any{"requests": requests.Load(), "terminalRestored": reflect.DeepEqual(before, after), "exitCode": code}), 0600)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err = os.Stat(filepath.Join(home, "theme-pty-release")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("physical terminal acknowledgement missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if requests.Load() != 2 || !reflect.DeepEqual(before, after) {
		t.Fatal("public Theme flow incomplete", requests.Load())
	}
}
