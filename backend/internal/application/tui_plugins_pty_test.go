package application

import (
	"context"
	"encoding/json"
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

func TestActualCLIPluginInstallMaskedOptionsReloadSkillAndTogglePTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY; ConPTY separate")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("native PTY unavailable")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	_ = os.WriteFile(filepath.Join(home, "disposable-plugins-pty"), []byte("owned"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_plugins_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_PLUGINS_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual plugin PTY %v %.7000s", err, output)
	}
	if !strings.Contains(string(output), `"terminalRestored": true`) {
		t.Fatal(string(output))
	}
}
func TestNativeCLIPluginPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_PLUGINS_PTY_HELPER") != "1" {
		t.Skip("owned PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-plugins-pty")); err != nil {
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
		if requests.Add(1) != 1 || !strings.Contains(string(body), "ACTUAL_PLUGIN_OPTION_North") {
			t.Error("public plugin next-turn consumer or local request violated", requests.Load())
		}
		response(w, "plugin-public-next", "", nil, "PLUGIN_PUBLIC_SKILL_NEXT_DONE_341")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Stderr = os.Stderr
	// Source's existing force flag exposes the true-gate auto-update choice
	// while this disposable profile retains its nonessential network opt-out.
	o.Environment["FORCE_AUTOUPDATE_PLUGINS"] = "1"
	o.Native.Environment["FORCE_AUTOUPDATE_PLUGINS"] = "1"
	prepareTerminalPluginFixture(t, o)
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}), 0600)
	_ = os.WriteFile(filepath.Join(home, "plugins-config.txt"), []byte(config), 0600)
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
	receipt := map[string]any{"terminalRestored": reflect.DeepEqual(before, after), "requests": requests.Load(), "exitCode": code}
	data, _ := json.Marshal(receipt)
	_ = os.WriteFile(filepath.Join(home, "plugins-receipt.json"), data, 0600)
	for {
		if _, err := os.Stat(filepath.Join(home, "plugins-termios-release")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("physical termios inspection never released fixture")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if requests.Load() != 1 || !reflect.DeepEqual(before, after) {
		t.Fatal(string(data))
	}
}
