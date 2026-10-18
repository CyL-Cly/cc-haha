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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
)

func TestActualCLIPluginBulkSelectionOrderNoConfigurationReloadSkillAndNextHumanPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix native PTY; ConPTY requires its own host")
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
	_ = os.WriteFile(filepath.Join(home, "disposable-plugin-bulk-pty"), []byte("owned"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_plugin_bulk_pty.py", binary, home)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=" + os.Getenv("PATH"), "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_PLUGIN_BULK_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual bulk CLI %v %.10000s", err, output)
	}
	t.Log(string(output))
}

func TestNativeCLIPluginBulkPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_PLUGIN_BULK_PTY_HELPER") != "1" {
		t.Skip("owned bulk PTY fixture")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-plugin-bulk-pty")); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		index := requests.Add(1)
		if index == 1 && !strings.Contains(string(body), "BULK_ACTUAL_ZETA") {
			t.Error("bulk installation never reached actual next Skill", string(body))
		}
		if index > 2 {
			t.Error("local plugin operation escaped to model", index)
		}
		marker := "PUBLIC_BULK_SKILL_DONE_884"
		if index == 2 {
			marker = "PUBLIC_BULK_HUMAN_DONE_884"
		}
		response(w, "public-bulk-next", "", nil, marker)
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Stderr = os.Stderr
	prepareTerminalBulkFixture(t, o.WorkDir, o.Environment["CLAUDE_CONFIG_DIR"])
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}), 0600)
	_ = os.WriteFile(filepath.Join(home, "bulk-config.txt"), []byte(config), 0600)
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
	_, _ = nativeTUIPresentationTerminalProbe(os.Stdin.Fd())
	after, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(config, "settings.json"))
	settings := obj(data)
	valid := string(obj(settings.Get("enabledPlugins")).Get("zeta@mkt")) == "true" && string(obj(settings.Get("enabledPlugins")).Get("alpha@mkt")) == "true" && settings.Get("pluginConfigs") == nil && string(obj(settings.Get("futureUser")).Get("keep")) == "true"
	receipt := map[string]any{"requests": requests.Load(), "exitCode": code, "terminalRestored": reflect.DeepEqual(before, after), "actualBulkSettingsNoPostConfiguration": valid}
	_ = os.WriteFile(filepath.Join(home, "bulk-receipt.json"), raw(receipt), 0600)
	for {
		if _, err = os.Stat(filepath.Join(home, "bulk-termios-release")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("slave termios probe never released helper")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if requests.Load() != 2 || !valid || !reflect.DeepEqual(before, after) {
		t.Fatal(string(raw(receipt)))
	}
}
