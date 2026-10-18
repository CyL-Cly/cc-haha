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

func TestActualCLIConfigDiffTerminalAutoFileSaveNextHumanAndHeldGlobalExitPTY(t *testing.T) {
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
	_ = os.WriteFile(filepath.Join(home, "disposable-ide-diff-pty"), []byte("owned"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/terminal_ide_diff_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_IDE_DIFF_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual IDE diff public PTY %v %.12000s", err, output)
	}
	if !strings.Contains(string(output), `"terminalRestored": true`) {
		t.Fatal(string(output))
	}
	t.Log(strings.TrimSpace(string(output)))
}

func TestNativeCLIIDEDiffPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_IDE_DIFF_PTY_HELPER") != "1" {
		t.Skip("owned IDE diff PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-ide-diff-pty")); err != nil {
		t.Fatal(err)
	}
	peer := newTerminalDiffPhysicalPeer(t, "public")
	peer.receiptPath = filepath.Join(home, "ide-diff-wire.json")
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(r.Body)
		index := requests.Add(1)
		_ = os.WriteFile(filepath.Join(home, "ide-diff-model.json"), raw(map[string]int32{"requests": index}), 0600)
		switch index {
		case 1, 4, 8:
			response(w, "public-ide-diff-read", "Read", map[string]any{"file_path": peer.path}, "")
		case 2:
			response(w, "public-ide-diff-terminal", "Edit", map[string]any{"file_path": peer.path, "old_string": "old", "new_string": "new"}, "")
		case 3:
			response(w, "public-ide-diff-terminal-done", "", nil, "IDE_DIFF_TERMINAL_ACTUAL_DONE_526")
		case 5:
			response(w, "public-ide-diff-auto", "Edit", map[string]any{"file_path": peer.path, "old_string": "new", "new_string": "new-auto"}, "")
		case 6:
			if !strings.Contains(string(obj(body).Get("messages")), "The user modified your proposed changes before accepting them.") {
				t.Error("same Harness did not receive the actual user-modified tool result")
			}
			response(w, "public-ide-diff-auto-done", "", nil, "IDE_DIFF_AUTO_ACTUAL_DONE_526")
		case 7:
			response(w, "public-ide-diff-next", "", nil, "IDE_DIFF_NEXT_HUMAN_DONE_526")
		case 9:
			response(w, "public-ide-diff-held", "Edit", map[string]any{"file_path": peer.path, "old_string": "USER_IDE_PUBLIC_SAVE_526", "new_string": "must-never-write-after-exit"}, "")
		default:
			t.Error("extra actual model request after held permission/exit", index)
			response(w, "public-ide-diff-unexpected", "", nil, "UNEXPECTED_EXTRA_DIFF_REQUEST")
		}
	}))
	defer provider.Close()
	o := terminalDiffNativeSessionOptions(t, provider.URL, peer)
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	settings := filepath.Join(config, "settings.json")
	global := filepath.Join(config, ".claude.json")
	_ = os.WriteFile(filepath.Join(home, "ide-diff-paths.json"), raw(map[string]any{"file": peer.path, "settings": settings, "global": global}), 0600)
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
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
	select {
	case <-peer.closed:
	case <-ctx.Done():
		t.Fatal("actual IDE connection not joined at global exit", ctx.Err())
	}
	data, _ := os.ReadFile(peer.path)
	user, _ := os.ReadFile(settings)
	saved, _ := os.ReadFile(global)
	receipt := map[string]any{"terminalRestored": reflect.DeepEqual(before, after), "physicalCleanupJoined": peer.count("openDiff") == 2 && peer.count("close_tab") == 2, "actualSavedBytes": string(data) == "head\nUSER_IDE_PUBLIC_SAVE_526\nlast\n", "unknownFieldsPreserved": string(obj(obj(saved).Get("futureDiffGlobal")).Get("keep")) == "true" && string(obj(obj(user).Get("futureDiffUser")).Get("keep")) == "true", "requests": requests.Load(), "exitCode": code}
	_ = os.WriteFile(filepath.Join(home, "ide-diff-receipt.json"), raw(receipt), 0600)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err = os.Stat(filepath.Join(home, "ide-diff-release")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("terminal inspection acknowledgement missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if requests.Load() != 9 || !reflect.DeepEqual(before, after) || peer.count("openDiff") != 2 || peer.count("close_tab") != 2 || string(data) != "head\nUSER_IDE_PUBLIC_SAVE_526\nlast\n" {
		t.Fatal(receipt)
	}
}
