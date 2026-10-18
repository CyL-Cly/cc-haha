package application

import (
	"bytes"
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

func TestActualPublicCLIContextEffortStatsPNGAndNextHumanPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("actual Unix PTY; ConPTY requires Windows host")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("actual PTY Python unavailable")
	}
	binary, _ := os.Executable()
	home := t.TempDir()
	if err = os.WriteFile(filepath.Join(home, "owned-context-stats-pty"), []byte("isolated"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, "testdata/terminal_context_stats_pty.py", binary, home)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_CONTEXT_STATS_PTY_HELPER=1"}
	capturePublicGoCoverage(t, command)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("actual RunCommand context/effort/stats", err, string(output))
	}
	if !strings.Contains(string(output), `"terminalRestored": true`) || !strings.Contains(string(output), `"actualPNGClipboard": true`) {
		t.Fatal("actual product receipt missing", string(output))
	}
}
func TestNativeTerminalContextStatsPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_CONTEXT_STATS_PTY_HELPER") != "1" {
		t.Skip("owned isolated public CLI helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "owned-context-stats-pty")); err != nil {
		t.Fatal(err)
	}
	var ordinary, counting atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/v1/messages/count_tokens" {
			counting.Add(1)
			w.Header().Set("content-type", "application/json")
			io.WriteString(w, `{"input_tokens":37}`)
			return
		}
		n := ordinary.Add(1)
		for _, command := range []string{"/context", "/effort", "/stats"} {
			if bytes.Contains(body, []byte(command)) {
				t.Error("local command leaked to inference", string(body))
			}
		}
		effort := "low"
		if n == 2 {
			effort = "xhigh"
		}
		if !bytes.Contains(body, []byte(`"effort":"`+effort+`"`)) {
			t.Error("real next model lost current effort", n, string(body))
		}
		response(w, "context-stats-pty", "", nil, []string{"PUBLIC_EFFORT_LOW_MODEL_COMPLETE", "PUBLIC_EFFORT_XHIGH_MODEL_COMPLETE"}[min(int(n)-1, 1)])
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Stderr = os.Stderr
	bin := filepath.Join(home, "private-screenshot-bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\npath=$(printf '%s' \"$2\" | /usr/bin/sed -n 's/.*POSIX file \"\\([^\"]*\\)\".*/\\1/p')\n/bin/cp \"$path\" \"$SCREENSHOT_RECEIPT\"\nprintf '%s' \"$2\" > \"$SCREENSHOT_ARGS\"\n"
	if err := os.WriteFile(filepath.Join(bin, "osascript"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["PATH"] = bin + ":/usr/bin:/bin"
		env["SCREENSHOT_RECEIPT"] = filepath.Join(home, "stats-screenshot.png")
		env["SCREENSHOT_ARGS"] = filepath.Join(home, "stats-screenshot-args.txt")
		env["ANTHROPIC_DEFAULT_SONNET_MODEL"] = "fixture-model"
		env["ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES"] = "effort,xhigh_effort"
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	seedTerminalStats(t, config, o.Native.Now())
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"future":{"preserve":37}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "stats-config-dir.txt"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	environment := []string{}
	for key, value := range o.Environment {
		environment = append(environment, key+"="+value)
	}
	environment = append(environment, "TERM=xterm-256color")
	code, err := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, environment)
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
	saved, err := os.ReadFile(filepath.Join(config, "settings.json"))
	if err != nil || bytes.Contains(saved, []byte(`"effortLevel"`)) || !bytes.Contains(saved, []byte(`"preserve"`)) {
		t.Fatal("auto did not preserve physical user settings", err, string(saved))
	}
	receipt := map[string]any{"terminalRestored": reflect.DeepEqual(before, after), "ordinary": ordinary.Load(), "counting": counting.Load(), "exitCode": code}
	data, _ := json.Marshal(receipt)
	if err = os.WriteFile(filepath.Join(home, "context-stats-receipt.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err = os.Stat(filepath.Join(home, "context-stats-pty-release")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("physical parent inspection not acknowledged")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ordinary.Load() != 2 || counting.Load() == 0 {
		t.Fatal("actual context/local command/model consumers", receipt)
	}
}
