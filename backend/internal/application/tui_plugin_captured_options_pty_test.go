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

func TestActualCLIPluginCapturedOptionsAfterSchemaUpdateAndUninstallPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY; actual ConPTY host required")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("native PTY unavailable")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"update", "uninstall"} {
		t.Run(operation, func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(filepath.Join(home, "disposable-captured-options-pty"), []byte(operation), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 28*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, python, "testdata/tui_plugin_captured_options_pty.py", binary, home, operation)
			cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=" + os.Getenv("PATH"), "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_PLUGIN_CAPTURED_PTY_HELPER=" + operation}
			capturePublicGoCoverage(t, cmd)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("actual captured options CLI %v %.12000s", err, output)
			}
			t.Log(string(output))
		})
	}
}

func TestNativeCLIPluginCapturedOptionsPTYFixtureProcess(t *testing.T) {
	operation := os.Getenv("CC_HAHA_PLUGIN_CAPTURED_PTY_HELPER")
	if operation != "update" && operation != "uninstall" && operation != "raw" {
		t.Skip("owned captured plugin options PTY fixture")
	}
	home := os.Getenv("HOME")
	if data, err := os.ReadFile(filepath.Join(home, "disposable-captured-options-pty")); err != nil || string(data) != operation {
		t.Fatal("unowned captured fixture", err)
	}
	var requests, invalid atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		if requests.Add(1) != 1 || !strings.Contains(string(body), "CAPTURED_OPTIONS_NEXT_REQUEST") {
			invalid.Add(1)
		}
		response(w, "captured-options-actual-next", "", nil, "CAPTURED_OPTIONS_NEXT_DONE")
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	prepareTerminalBulkFixture(t, o.WorkDir, o.Environment["CLAUDE_CONFIG_DIR"])
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(config, ".claude.json"), raw(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}))
	if operation == "raw" {
		write(filepath.Join(o.WorkDir, "market/plugins/zeta/.claude-plugin/plugin.json"), terminalRawPluginManifest())
		write(filepath.Join(config, "settings.json"), terminalRawPluginOldSettings())
	}
	write(filepath.Join(home, "captured-paths.json"), raw(map[string]string{"config": config, "plugin": filepath.Join(o.WorkDir, "market/plugins/zeta")}))
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Second)
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
	data, err := os.ReadFile(filepath.Join(config, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	valid := obj(obj(obj(obj(data).Get("pluginConfigs")).Get("zeta@mkt")).Get("options")).String("region") == "Captured region" && string(obj(obj(data).Get("futureUser")).Get("keep")) == "true"
	if operation == "raw" {
		valid = terminalRawPluginOptionsValid(obj(data))
	}
	write(filepath.Join(home, "captured-receipt.json"), raw(map[string]any{"operation": operation, "requests": requests.Load(), "invalid": invalid.Load(), "exitCode": code, "actualCapturedOptions": valid, "terminalRestored": reflect.DeepEqual(before, after)}))
	for {
		if _, err = os.Stat(filepath.Join(home, "captured-termios-release")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("physical restoration receipt not released")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if requests.Load() != 1 || invalid.Load() != 0 || !valid || !reflect.DeepEqual(before, after) {
		t.Fatal("actual captured options lifecycle", requests.Load(), invalid.Load(), string(data))
	}
}
