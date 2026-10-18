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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
)

func TestActualPublicCLIClearAliasesNextHumanAndRestoredPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("physical Unix PTY; Windows ConPTY requires host")
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
	if err = os.WriteFile(filepath.Join(home, "owned-clear-pty"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/terminal_clear_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "CC_HAHA_CLEAR_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), `"terminalRestored": true`) {
		t.Fatal("actual public clear", err, string(output))
	}
}

func TestNativeTerminalClearPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_CLEAR_PTY_HELPER") != "1" {
		t.Skip("owned isolated Clear CLI helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "owned-clear-pty")); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var sids, prompts []string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil || r.Method != "POST" || r.Header.Get("x-api-key") != "fake-only-loopback" {
			t.Error("Actual ordinary model transport", err, r.Method, r.Header)
		}
		mu.Lock()
		count := len(sids) + 1
		for before := 1; before < count; before++ {
			if strings.Contains(string(body), "PUBLIC_CLEAR_HUMAN_"+strconv.Itoa(before)) {
				t.Error("Old human history leaked into next actual clear request", string(body))
			}
		}
		current := "PUBLIC_CLEAR_HUMAN_" + strconv.Itoa(count)
		if !strings.Contains(string(body), current) || count > 1 && (!strings.Contains(string(body), "<command-name>/clear</command-name>") || !strings.Contains(string(body), "<local-command-stdout></local-command-stdout>")) {
			t.Error("Canonical local replay or new human absent", string(body))
		}
		sid := r.Header.Get("X-Claude-Code-Session-Id")
		encoded := obj(obj(body).Get("metadata")).String("user_id")
		if obj([]byte(encoded)).String("session_id") != sid || sid == "" {
			t.Error("Actual header/metadata lost current session", sid, encoded)
		}
		for _, previous := range sids {
			if previous == sid {
				t.Error("Actual alias failed to regenerate session", sids, sid)
			}
		}
		sids, prompts = append(sids, sid), append(prompts, current)
		mu.Unlock()
		response(w, "clear-pty-"+strconv.Itoa(count), "", nil, "PUBLIC_CLEAR_HUMAN_COMPLETE_"+strconv.Itoa(count))
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Stderr = os.Stderr
	o.Interactive, o.ForegroundShellProgress = true, true
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
	env = append(env, "TERM=xterm-256color")
	code, err := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, env)
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
	mu.Lock()
	actualSIDs, actualPrompts := append([]string{}, sids...), append([]string{}, prompts...)
	mu.Unlock()
	if len(actualSIDs) != 4 || len(actualPrompts) != 4 {
		t.Fatal("Actual public alias consumers missing", actualSIDs, actualPrompts)
	}
	encoded, _ := json.Marshal(map[string]any{"code": code, "terminalRestored": reflect.DeepEqual(before, after), "sids": actualSIDs, "prompts": actualPrompts})
	if err = os.WriteFile(filepath.Join(home, "clear-receipt.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(home, "clear-pty-release")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("PTY parent did not inspect restored terminal")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
