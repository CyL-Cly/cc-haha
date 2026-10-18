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
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
)

func TestActualCLIBtwBusyForkScrollCancelWriteNextHumanAndGlobalClosePTY(t *testing.T) {
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
	if err = os.WriteFile(filepath.Join(home, "disposable-btw-pty"), []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 32*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_btw_pty.py", binary, home)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=" + os.Getenv("PATH"), "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_BTW_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Actual BTW public CLI %v %.10000s", err, output)
	}
	t.Log(string(output))
}
func TestNativeCLIBtwPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_BTW_PTY_HELPER") != "1" {
		t.Skip("Owned BTW PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-btw-pty")); err != nil {
		t.Fatal(err)
	}
	var requests, invalid, sideCanceled, mainCanceled atomic.Int32
	written := filepath.Join(home, "btw-actual-write.txt")
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		n := requests.Add(1)
		side := bytes.Contains(body, []byte("This is a side question from the user"))
		if side {
			if !bytes.Contains(body, []byte("BTW_HELD_MAIN")) {
				invalid.Add(1)
			}
		} else if n >= 4 {
			if !bytes.Contains(body, []byte("Usage: /btw")) || bytes.Contains(body, []byte("BTW_SIDE_")) {
				invalid.Add(1)
			}
		}
		marker := func(name string) {
			if err := os.WriteFile(filepath.Join(home, name), []byte("actual HTTP"), 0600); err != nil {
				invalid.Add(1)
			}
		}
		switch n {
		case 1:
			if side {
				invalid.Add(1)
			}
			marker("btw-main-started")
			for {
				if _, err := os.Stat(filepath.Join(home, "btw-release-main")); err == nil {
					break
				}
				select {
				case <-r.Context().Done():
					mainCanceled.Add(1)
					return
				case <-time.After(10 * time.Millisecond):
				}
			}
			response(w, "btw-first", "", nil, "BTW_PARENT_FIRST_DONE")
		case 2:
			if !side {
				invalid.Add(1)
			}
			marker("btw-side-first-started")
			<-r.Context().Done()
			sideCanceled.Add(1)
			marker("btw-side-first-cancelled")
		case 3:
			if !side {
				invalid.Add(1)
			}
			text := ""
			for i := 0; i < 25; i++ {
				line := "BTW_SIDE_ANSWER_LINE_" + string(rune('A'+i))
				if i == 0 {
					line = "**" + line + "**"
				}
				if i == 9 {
					line = "`" + line + "`"
				}
				text += line + "\n"
			}
			response(w, "btw-answer", "", nil, text)
		case 4:
			if side {
				invalid.Add(1)
			}
			response(w, "btw-write", "Write", map[string]any{"file_path": written, "content": "BTW_SHARED_HARNESS_WRITE"}, "")
		case 5:
			if !bytes.Contains(body, []byte("BTW_SHARED_HARNESS_WRITE")) {
				invalid.Add(1)
			}
			response(w, "btw-next", "", nil, "BTW_NEXT_HUMAN_DONE")
		case 6:
			marker("btw-close-main-started")
			<-r.Context().Done()
			mainCanceled.Add(1)
			marker("btw-close-main-cancelled")
		case 7:
			if !side {
				invalid.Add(1)
			}
			marker("btw-close-side-started")
			<-r.Context().Done()
			sideCanceled.Add(1)
			marker("btw-close-side-cancelled")
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
	if err := os.WriteFile(global, []byte(`{"btwUseCount":0,"futureBtw":{"keep":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "btw-paths.json"), raw(map[string]any{"global": global, "written": written}), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 27*time.Second)
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
	if err != nil || string(data) != "BTW_SHARED_HARNESS_WRITE" {
		t.Fatal(err, string(data))
	}
	receipt := map[string]any{"requests": requests.Load(), "invalid": invalid.Load(), "sideCanceled": sideCanceled.Load(), "mainCanceled": mainCanceled.Load(), "exitCode": code, "actualWrite": true, "terminalRestored": reflect.DeepEqual(before, after)}
	if err = os.WriteFile(filepath.Join(home, "btw-receipt.json"), raw(receipt), 0600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err = os.Stat(filepath.Join(home, "btw-termios-release")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("Physical termios restoration not acknowledged")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
