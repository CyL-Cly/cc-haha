package application

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
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

func TestActualCLIAddDirectoryRememberFormAndNextWritePTY(t *testing.T) {
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
	_ = os.WriteFile(filepath.Join(home, "disposable-directory-pty"), []byte("owned"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_directory_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_DIRECTORY_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual directory PTY %v %.7000s", err, output)
	}
	if !strings.Contains(string(output), `"terminalRestored": true`) {
		t.Fatal(string(output))
	}
}
func TestNativeCLIAddDirectoryPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_DIRECTORY_PTY_HELPER") != "1" {
		t.Skip("owned PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-directory-pty")); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	var directory, target string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		switch requests.Add(1) {
		case 1:
			if !strings.Contains(string(body), directory) || !strings.Contains(string(body), "Additional working directories") {
				t.Error("next actual query context omitted directory")
			}
			response(w, "public-directory-write", "Write", map[string]any{"file_path": target, "content": "PUBLIC_ADDED_DIRECTORY_WRITE"}, "")
		case 3:
			if strings.Contains(string(obj(body).Get("system")), directory) {
				t.Error("removed directory retained in actual public next context")
			}
			response(w, "public-removed-directory-write", "Write", map[string]any{"file_path": target, "content": "PUBLIC_REMOVED_DIRECTORY_MUST_NOT_WRITE"}, "")
		case 4:
			response(w, "public-directory-removed-done", "", nil, "DIRECTORY_PUBLIC_REMOVED_DONE_462")
		default:
			response(w, "public-directory-done", "", nil, "DIRECTORY_PUBLIC_NEXT_DONE_462")
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, errors.New("disposable fixture keychain unavailable")
	}
	o.Stderr = os.Stderr
	o.Arguments.SystemPrompt = ""
	o.Arguments.PermissionMode = "acceptEdits"
	o.Arguments.Tools = []string{"Read,Write"}
	directory = filepath.Join(o.Native.Home, "external-workspace")
	target = filepath.Join(directory, "output.txt")
	_ = os.MkdirAll(directory, 0700)
	second := filepath.Join(o.Native.Home, "session-workspace")
	_ = os.MkdirAll(second, 0700)
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	o.Native.Environment["CC_HAHA_CLI_RELEASE_API"] = peer.URL
	_ = os.MkdirAll(filepath.Join(o.WorkDir, ".claude"), 0700)
	_ = os.MkdirAll(config, 0700)
	_ = os.WriteFile(filepath.Join(o.WorkDir, ".claude", "settings.local.json"), []byte(`{"futureLocal":{"keep":true},"permissions":{"allow":["Read"]}}`), 0600)
	_ = os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}), 0600)
	_ = os.WriteFile(filepath.Join(home, "directory-paths.json"), raw(map[string]any{"directory": directory, "second": second, "target": target, "settings": filepath.Join(o.WorkDir, ".claude", "settings.local.json")}), 0600)
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
	_ = os.WriteFile(filepath.Join(home, "directory-receipt.json"), data, 0600)
	inspectionDeadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(home, "directory-pty-release")); err == nil {
			break
		}
		if time.Now().After(inspectionDeadline) {
			t.Fatal("physical terminal inspection acknowledgement missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if requests.Load() != 4 || !reflect.DeepEqual(before, after) {
		t.Fatal(string(data))
	}
}
