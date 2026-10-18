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

func TestActualCLIPluginErrorsPendingFailureRefreshRemoveLateClosedPanelAndNextHumanPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY, Windows host separate")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("private native PTY unavailable")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	_ = os.WriteFile(filepath.Join(home, "disposable-plugin-errors-pty"), []byte("owned"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_plugin_errors_pty.py", binary, home)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=" + os.Getenv("PATH"), "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_PLUGIN_ERRORS_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual public Plugin Errors %v %.10000s", err, output)
	}
	t.Log(string(output))
}
func TestNativeCLIPluginErrorsPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_PLUGIN_ERRORS_PTY_HELPER") != "1" {
		t.Skip("owned Errors PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-plugin-errors-pty")); err != nil {
		t.Fatal(err)
	}
	var requests, marketRequests atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		requests.Add(1)
		response(w, "public-errors-next", "", nil, "PUBLIC_ERRORS_NEXT_HUMAN_DONE_528")
	}))
	defer model.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	market := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		marketRequests.Add(1)
		name := strings.TrimPrefix(r.URL.Path, "/")
		_ = os.WriteFile(filepath.Join(home, "errors-started-"+name), []byte("physical GET held"), 0600)
		for {
			if _, err := os.Stat(filepath.Join(home, "errors-release-"+name)); err == nil {
				break
			}
			select {
			case <-r.Context().Done():
				return
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
		w.WriteHeader(503)
		_, _ = w.Write([]byte("owned private marketplace failure"))
		_ = os.WriteFile(filepath.Join(home, "errors-replied-"+name), []byte("actual 503 sent"), 0600)
	}))
	defer market.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Stderr = os.Stderr
	prepareTerminalPluginFixture(t, o)
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	extra := map[string]any{"ghost": map[string]any{"source": map[string]any{"source": "url", "url": market.URL + "/ghost"}}, "late": map[string]any{"source": map[string]any{"source": "url", "url": market.URL + "/late"}}}
	user := filepath.Join(config, "settings.json")
	project := filepath.Join(o.WorkDir, ".claude", "settings.json")
	for _, path := range []string{user, project} {
		_ = os.MkdirAll(filepath.Dir(path), 0700)
		_ = os.WriteFile(path, raw(map[string]any{"futureSource": map[string]any{"keep": true}, "extraKnownMarketplaces": extra, "enabledPlugins": map[string]any{"bad@ghost": true}}), 0600)
	}
	_ = os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}), 0600)
	_ = os.WriteFile(filepath.Join(home, "errors-paths.json"), raw(map[string]string{"user": user, "project": project}), 0600)
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	code, err := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, os.Environ())
	if err != nil || code != 0 {
		t.Fatal(code, err)
	}
	_, _ = nativeTUIPresentationTerminalProbe(os.Stdin.Fd())
	after, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	removed := true
	for _, path := range []string{user, project} {
		data, _ := os.ReadFile(path)
		settings := obj(data)
		removed = removed && obj(settings.Get("extraKnownMarketplaces")).Get("ghost") == nil && obj(settings.Get("extraKnownMarketplaces")).Get("late") != nil && obj(settings.Get("enabledPlugins")).Get("bad@ghost") == nil && string(obj(settings.Get("futureSource")).Get("keep")) == "true"
	}
	receipt := map[string]any{"requests": requests.Load(), "marketRequests": marketRequests.Load(), "exitCode": code, "terminalRestored": reflect.DeepEqual(before, after), "actualScopesRemovedUnknownPreserved": removed}
	_ = os.WriteFile(filepath.Join(home, "errors-receipt.json"), raw(receipt), 0600)
	for {
		if _, err := os.Stat(filepath.Join(home, "errors-termios-release")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("native termios proof never released helper")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if requests.Load() != 1 || marketRequests.Load() != 2 || !removed || !reflect.DeepEqual(before, after) {
		t.Fatal(receipt)
	}
}
