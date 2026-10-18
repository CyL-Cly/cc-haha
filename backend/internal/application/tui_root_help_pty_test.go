package application

import (
	"context"
	"encoding/json"
	"fmt"
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

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/charmbracelet/x/term"
)

func TestActualCLIRootShortcutsFullscreenSettingsFlipsCanonicalAndNextHumanPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native Unix PTY; ConPTY requires separate host")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err = os.WriteFile(filepath.Join(home, "owned-root-help"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_root_help_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "TMPDIR=" + home, "TERM=xterm-256color", "COLORTERM=truecolor", "CC_HAHA_ROOT_HELP_PTY=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), `"terminalRestored": true`) {
		t.Fatal("actual fullscreen Help public consumer", err, string(output))
	}
}

func TestNativeRootHelpPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_ROOT_HELP_PTY") != "1" {
		t.Skip("owned private fullscreen Help fixture")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "owned-root-help")); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		ordinal := calls.Add(1)
		expectedHelp := 0
		if ordinal == 3 {
			expectedHelp = 1
		}
		if ordinal > 3 || !strings.Contains(string(body), fmt.Sprintf("ROOT_HELP_HUMAN_%d", ordinal)) || strings.Count(string(body), "<command-name>/help</command-name>") != expectedHelp || strings.Count(string(body), "Help dialog dismissed") != expectedHelp || strings.Contains(string(body), "Claude understands your codebase") {
			t.Error("actual fullscreen completion or same Harness history differs", ordinal, string(body))
		}
		output := fmt.Sprintf("ROOT_HELP_DONE_%d", ordinal)
		if ordinal == 1 {
			rows := []string{}
			for i := 0; i < 70; i++ {
				rows = append(rows, fmt.Sprintf("FULLSCREEN_TRANSCRIPT_%03d", i))
			}
			output = strings.Join(rows, "\n") + "\n" + output
		}
		response(w, fmt.Sprintf("fullscreen-help-%d", ordinal), "", nil, output)
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive, o.ForegroundShellProgress = true, true
	o.Stderr = os.Stderr
	o.Environment["CLAUDE_CODE_NO_FLICKER"] = "0"
	o.Native.Environment["CLAUDE_CODE_NO_FLICKER"] = "0"
	commands := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "commands")
	if err := os.MkdirAll(commands, 0700); err != nil {
		t.Fatal(err)
	}
	// The original detector selects this directory only when a settings file
	// exists at mount. Match the actual Source fixture's initial profile.
	if err := os.WriteFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		data := fmt.Sprintf("---\ndescription: Actual fullscreen custom command %02d\n---\nThis command is read-only in Help.\n", i)
		if err := os.WriteFile(filepath.Join(commands, fmt.Sprintf("owned-%02d.md", i)), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "root-help-settings-path"), []byte(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json")), 0600); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], ".claude.json")
	if err := os.WriteFile(global, raw(map[string]any{"hasCompletedOnboarding": true, "futureRootView": map[string]any{"keep": true}, "showExpandedTodos": false, "showSpinnerTree": false}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "root-help-global-path"), []byte(global), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	var env []string
	for key, value := range o.Environment {
		env = append(env, key+"="+value)
	}
	env = append(env, "TERM=xterm-256color", "COLORTERM=truecolor")
	code, err := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, env)
	if err != nil || code != 0 {
		t.Fatal(code, err)
	}
	if _, err = nativeTUIPresentationTerminalProbe(os.Stdin.Fd()); err != nil {
		t.Fatal(err)
	}
	after, err := term.GetState(os.Stdin.Fd())
	if err != nil || calls.Load() != 3 {
		t.Fatal(err, calls.Load())
	}
	o.Arguments.SessionID = "7a31e4fe-90b3-4b63-a2f5-02b767fb185f"
	second, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal("Actual fresh session after global view save", err)
	}
	view, viewErr := second.TerminalExpandedView(ctx)
	data, readErr := os.ReadFile(global)
	var persisted protocol.JSONObject
	parseErr := persisted.UnmarshalJSON(data)
	closeErr := second.Close(ctx)
	if viewErr != nil || readErr != nil || parseErr != nil || closeErr != nil || view != "tasks" || string(obj(persisted.Get("futureRootView")).Get("keep")) != "true" || calls.Load() != 3 {
		t.Fatal("Saved view restart/unknown fields/zero extra model", view, viewErr, readErr, parseErr, closeErr, string(data), calls.Load())
	}
	receipt, _ := json.Marshal(map[string]any{"code": code, "requests": calls.Load(), "terminalRestored": reflect.DeepEqual(before, after)})
	if err = os.WriteFile(filepath.Join(home, "root-help-receipt.json"), receipt, 0600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err = os.Stat(filepath.Join(home, "root-help-restoration-inspected")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("live leader restoration inspection missing", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
