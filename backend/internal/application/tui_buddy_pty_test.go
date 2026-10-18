package application

import (
	"bytes"
	"context"
	"github.com/charmbracelet/x/term"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualCLIBuddyCardPetMuteHatchIntroWriteNextHumanAndRestorationPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix physical PTY")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Physical Python PTY unavailable")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err = os.WriteFile(filepath.Join(home, "disposable-buddy-pty"), []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 28*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_buddy_pty.py", binary, home)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=" + os.Getenv("PATH"), "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_BUDDY_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Actual Buddy public CLI %v %.10000s", err, output)
	}
	t.Log(string(output))
}

func TestNativeCLIBuddyPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_BUDDY_PTY_HELPER") != "1" {
		t.Skip("Owned Buddy PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-buddy-pty")); err != nil {
		t.Fatal(err)
	}
	var requests, invalid atomic.Int32
	written := filepath.Join(home, "actual-buddy-write.txt")
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		number := requests.Add(1)
		if bytes.Count(body, []byte("# Companion")) != 1 || !bytes.Contains(body, []byte("<command-name>/buddy")) {
			invalid.Add(1)
		}
		// Original processSlashCommand preserves display:system as two
		// local_command records. Its API normalizer forwards both tagged texts
		// once, including the pet and mute stdout visible in the real Message.
		if bytes.Count(body, []byte("<local-command-stdout>You pet Public Buddy! ♥</local-command-stdout>")) != 1 ||
			bytes.Count(body, []byte("<local-command-stdout>Public Buddy is now muted.</local-command-stdout>")) != 1 ||
			bytes.Count(body, []byte("<command-args>mute</command-args>")) != 1 {
			invalid.Add(1)
			t.Error("Actual Buddy next request lost or duplicated Source canonical pet/mute", string(body))
		}
		switch number {
		case 1:
			if !bytes.Contains(body, []byte("BUDDY_FIRST_HUMAN")) {
				invalid.Add(1)
			}
			response(w, "buddy-write", "Write", map[string]any{"file_path": written, "content": "BUDDY_PHYSICAL_WRITE"}, "")
		case 2:
			if !bytes.Contains(body, []byte("BUDDY_PHYSICAL_WRITE")) {
				invalid.Add(1)
			}
			response(w, "buddy-first", "", nil, "BUDDY_FIRST_DONE")
		case 3:
			if !bytes.Contains(body, []byte("BUDDY_NEXT_HUMAN")) {
				invalid.Add(1)
			}
			response(w, "buddy-next", "", nil, "BUDDY_NEXT_DONE")
		default:
			invalid.Add(1)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(config, ".claude.json")
	if err := os.WriteFile(global, []byte(`{"companion":{"name":"Public Buddy","personality":"Existing soul","hatchedAt":0,"appearanceSeed":"public-buddy-seed","futureSoul":{"keep":true}},"futureBuddy":{"keep":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "buddy-paths.json"), raw(map[string]any{"global": global, "written": written}), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 23*time.Second)
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
	data, err := os.ReadFile(written)
	if err != nil || string(data) != "BUDDY_PHYSICAL_WRITE" {
		t.Fatal("Actual next-Harness Write", err, string(data))
	}
	if err = os.WriteFile(filepath.Join(home, "buddy-receipt.json"), raw(map[string]any{"requests": requests.Load(), "invalid": invalid.Load(), "exitCode": code, "terminalRestored": reflect.DeepEqual(before, after), "actualWrite": true}), 0600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err = os.Stat(filepath.Join(home, "buddy-termios-release")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("Actual slave restoration unacknowledged")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
