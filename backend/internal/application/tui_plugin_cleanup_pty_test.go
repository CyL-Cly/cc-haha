package application

import (
	"context"
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

func TestActualCLIPluginCleanupKeepDeleteSharedProjectOverrideAndNextASKPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix native PTY; ConPTY needs its own host")
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
	_ = os.WriteFile(filepath.Join(home, "disposable-plugin-cleanup-pty"), []byte("owned"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_plugin_cleanup_pty.py", binary, home)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=" + os.Getenv("PATH"), "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_PLUGIN_CLEANUP_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual cleanup CLI %v %.15000s", err, output)
	}
	t.Log(string(output))
}

func TestNativeCLIPluginCleanupPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_PLUGIN_CLEANUP_PTY_HELPER") != "1" {
		t.Skip("owned cleanup PTY fixture")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-plugin-cleanup-pty")); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		n := requests.Add(1)
		if n > 3 {
			t.Error("local plugin operation escaped to model", n)
		}
		response(w, "cleanup-next", "", nil, []string{"", "PUBLIC_PLUGIN_KEEP_NEXT_925", "PUBLIC_PLUGIN_DELETE_NEXT_925", "PUBLIC_PLUGIN_PROJECT_NEXT_925"}[n])
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Stderr = os.Stderr
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
	write(filepath.Join(config, "settings.json"), []byte(`{"futureUser":{"keep":true},"pluginConfigs":{"zeta@mkt":{"options":{"region":"Zeta saved"}},"alpha@mkt":{"options":{"region":"Alpha saved"}},"other@mkt":{"options":{"keep":"other private config"}}}}`))
	marketPath := filepath.Join(o.WorkDir, "market", ".claude-plugin", "marketplace.json")
	data, err := os.ReadFile(marketPath)
	if err != nil {
		t.Fatal(err)
	}
	catalog := obj(data)
	entries := array(catalog.Get("plugins"))
	entries = append(entries, obj([]byte(`{"name":"shared","source":"./plugins/shared"}`)))
	catalog.Set("plugins", raw(entries))
	write(marketPath, raw(catalog))
	write(filepath.Join(o.WorkDir, "market", "plugins", "shared", ".claude-plugin", "plugin.json"), []byte(`{"name":"shared","version":"1.0.0"}`))
	for _, name := range []string{"zeta", "alpha", "shared"} {
		write(filepath.Join(config, "plugins", "data", name+"-mkt", "state"), []byte("PRIVATE_PERSISTENT_DATA_"+name))
	}
	write(filepath.Join(config, ".claude.json"), raw(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}))
	write(filepath.Join(home, "cleanup-paths.json"), raw(map[string]string{"config": config, "work": o.WorkDir}))
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
	data, _ = os.ReadFile(filepath.Join(config, "settings.json"))
	user := obj(data)
	data, _ = os.ReadFile(filepath.Join(o.WorkDir, ".claude", "settings.json"))
	project := obj(data)
	data, _ = os.ReadFile(filepath.Join(o.WorkDir, ".claude", "settings.local.json"))
	local := obj(data)
	_, kept := os.Stat(filepath.Join(config, "plugins", "data", "zeta-mkt", "state"))
	_, deleted := os.Stat(filepath.Join(config, "plugins", "data", "alpha-mkt", "state"))
	shared, sharedErr := os.ReadFile(filepath.Join(config, "plugins", "data", "shared-mkt", "state"))
	data, _ = os.ReadFile(filepath.Join(config, "plugins", "installed_plugins.json"))
	installed := obj(obj(data).Get("plugins"))
	scopes := array(installed.Get("shared@mkt"))
	valid := kept == nil && os.IsNotExist(deleted) && sharedErr == nil && strings.Contains(string(shared), "shared") && obj(user.Get("pluginConfigs")).Get("zeta@mkt") == nil && obj(user.Get("pluginConfigs")).Get("alpha@mkt") == nil && string(obj(user.Get("futureUser")).Get("keep")) == "true" && obj(user.Get("pluginConfigs")).Get("other@mkt") != nil && string(obj(project.Get("enabledPlugins")).Get("shared@mkt")) == "true" && string(obj(local.Get("enabledPlugins")).Get("shared@mkt")) == "false" && len(scopes) == 1 && scopes[0].String("scope") == "project"
	receipt := map[string]any{"exitCode": code, "requests": requests.Load(), "actualDataAndScopedSettings": valid, "terminalRestored": reflect.DeepEqual(before, after)}
	write(filepath.Join(home, "cleanup-receipt.json"), raw(receipt))
	for {
		if _, err = os.Stat(filepath.Join(home, "cleanup-termios-release")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("slave termios proof never released cleanup helper")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if requests.Load() != 3 || !valid || !reflect.DeepEqual(before, after) {
		t.Fatal(string(raw(receipt)), string(data))
	}
}
