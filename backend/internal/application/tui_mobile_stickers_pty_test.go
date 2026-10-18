package application

import (
	"bytes"
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

func TestActualCLIMobileQRPlatformAliasesStickersChildAndNextHumanPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY; real ConPTY separate")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Native PTY unavailable")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err = os.WriteFile(filepath.Join(home, "disposable-mobile-pty"), []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 28*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_mobile_stickers_pty.py", binary, home)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=" + os.Getenv("PATH"), "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_MOBILE_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Actual mobile/stickers CLI %v %.10000s", err, output)
	}
	t.Log(string(output))
}

func TestNativeCLIMobileStickersPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_MOBILE_PTY_HELPER") != "1" {
		t.Skip("Owned mobile/stickers PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-mobile-pty")); err != nil {
		t.Fatal(err)
	}
	var calls, invalid atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		if calls.Add(1) != 1 || !bytes.Contains(body, []byte("MOBILE_STICKERS_NEXT_HUMAN")) || !bytes.Contains(body, []byte("Opening sticker page in browser")) || !bytes.Contains(body, []byte("<command-name>/stickers")) {
			invalid.Add(1)
		}
		response(w, "mobile-stickers-next", "", nil, "MOBILE_STICKERS_NEXT_DONE")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	browser, receipt := filepath.Join(home, "private-mobile-browser"), filepath.Join(home, "mobile-browser-receipt")
	if err := os.WriteFile(browser, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" >> \"$MOBILE_BROWSER_RECEIPT\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["BROWSER"] = browser
		env["MOBILE_BROWSER_RECEIPT"] = receipt
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
	data, err := os.ReadFile(receipt)
	if err != nil || string(data) != "https://www.stickermule.com/claudecode\n" {
		t.Fatal("Actual browser child", err, string(data))
	}
	if err = os.WriteFile(filepath.Join(home, "mobile-receipt.json"), raw(map[string]any{"requests": calls.Load(), "invalid": invalid.Load(), "exitCode": code, "terminalRestored": reflect.DeepEqual(before, after), "actualBrowserChild": strings.Contains(string(data), "stickermule.com/claudecode")}), 0600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err = os.Stat(filepath.Join(home, "mobile-termios-release")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("Actual terminal restoration unacknowledged")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
