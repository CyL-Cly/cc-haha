//go:build !windows

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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
)

func TestActualPublicCLIStatusLineSettingsVimToolsHeldExitAndRestoredPTY(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err = os.WriteFile(filepath.Join(home, "owned-statusline-pty"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, "testdata/terminal_statusline_pty.py", binary, home)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "CC_HAHA_STATUSLINE_PTY_HELPER=1"}
	capturePublicGoCoverage(t, command)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), `"terminalRestored": true`) {
		t.Fatal("actual public StatusLine", err, string(output))
	}
	t.Log(strings.TrimSpace(string(output)))
}

func TestNativeTerminalStatusLinePTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_STATUSLINE_PTY_HELPER") != "1" {
		t.Skip("owned private StatusLine PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "owned-statusline-pty")); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	requests := 0
	var actualFile string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil || r.Header.Get("x-api-key") != "fake-only-loopback" {
			t.Error("actual same Harness transport", err, r.Header)
		}
		mu.Lock()
		defer mu.Unlock()
		requests++
		switch requests {
		case 1:
			if !strings.Contains(string(body), "PUBLIC_STATUSLINE_WRITE") {
				t.Error("first human missing", string(body))
			}
			response(w, "statusline-write", "Write", map[string]any{"file_path": actualFile, "content": "PUBLIC_STATUSLINE_PHYSICAL_DATA\n"}, "")
		case 2:
			if !strings.Contains(string(body), "PUBLIC_STATUSLINE_PHYSICAL_DATA") {
				t.Error("actual Write result missing", string(body))
			}
			response(w, "statusline-done", "", nil, "PUBLIC_STATUSLINE_WRITE_DONE")
		case 3:
			if !strings.Contains(string(body), "PUBLIC_STATUSLINE_NEXT_HUMAN") {
				t.Error("next human missing", string(body))
			}
			response(w, "statusline-read", "Read", map[string]any{"file_path": actualFile}, "")
		case 4:
			if !strings.Contains(string(body), "PUBLIC_STATUSLINE_PHYSICAL_DATA") {
				t.Error("actual Read result missing", string(body))
			}
			response(w, "statusline-next", "", nil, "PUBLIC_STATUSLINE_NEXT_DONE")
		default:
			t.Error("unowned model request", requests)
			http.Error(w, "unowned", 500)
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	o.Stderr = os.Stderr
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["NO_PROXY"] = "127.0.0.1,localhost"
	}
	actualFile = filepath.Join(o.WorkDir, "statusline-actual-tool.txt")
	config, nativeHome := o.Environment["CLAUDE_CONFIG_DIR"], o.Environment["HOME"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	first, held := filepath.Join(nativeHome, "statusline-first.py"), filepath.Join(nativeHome, "statusline-held.py")
	program := "#!/usr/bin/env python3\nimport json,os,sys,time\nx=json.load(sys.stdin)\nwith open(os.environ['HOME']+'/public-statusline-inputs.jsonl','a') as f:f.write(json.dumps(x)+'\\n')\n"
	if err := os.WriteFile(first, []byte(program+"print('PUBLIC_STATUSLINE_ONE '+x.get('vim',{}).get('mode','NO_VIM'))\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(held, []byte(program+"with open(os.environ['HOME']+'/statusline-held-ready','w') as f:f.write(str(os.getpid()))\ntime.sleep(60)\nwith open(os.environ['HOME']+'/forbidden-late-statusline','w') as f:f.write('late')\nprint('FORBIDDEN_LATE_STATUSLINE')\n"), 0700); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(config, "settings.json")
	if err := os.WriteFile(settingsPath, raw(map[string]any{"statusLine": map[string]any{"type": "command", "command": "'" + first + "'", "padding": 2}}), 0600); err != nil {
		t.Fatal(err)
	}
	seedTerminalReleaseWarmFixture(t, o)
	global := map[string]any{}
	data, err := os.ReadFile(filepath.Join(config, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &global); err != nil {
		t.Fatal(err)
	}
	global["editorMode"] = "vim"
	global["projects"] = map[string]any{o.Native.CWD: map[string]any{"hasTrustDialogAccepted": true}}
	if err = os.WriteFile(filepath.Join(config, ".claude.json"), raw(global), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "statusline-paths.json"), raw(map[string]any{"settings": settingsPath, "home": nativeHome, "normalScript": first, "normal": "'" + first + "'", "held": "'" + held + "'"}), 0600); err != nil {
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
	actualRequests := requests
	mu.Unlock()
	file, err := os.ReadFile(actualFile)
	if err != nil || string(file) != "PUBLIC_STATUSLINE_PHYSICAL_DATA\n" {
		t.Fatal("actual tool artifact", err, string(file))
	}
	if actualRequests != 4 {
		t.Fatal("actual same Harness requests", actualRequests)
	}
	if _, err = os.Stat(filepath.Join(nativeHome, "forbidden-late-statusline")); !os.IsNotExist(err) {
		t.Fatal("retired statusline executed late physical write", err)
	}
	if err = os.WriteFile(filepath.Join(home, "statusline-pty-receipt.json"), raw(map[string]any{"code": code, "terminalRestored": reflect.DeepEqual(before, after), "actualModelRequests": actualRequests, "actualToolArtifact": string(file)}), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err = os.Stat(filepath.Join(home, "statusline-pty-release")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("private PTY parent did not inspect restored terminal")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
