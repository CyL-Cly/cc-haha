package application

import (
	"context"
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

	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/charmbracelet/x/term"
)

func TestActualPublicCLIInternalTagProfileBlockedBySchemaGuardPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native Unix PTY; Windows needs ConPTY host")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("native Python PTY unavailable")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "owned-tag-pty"), []byte("isolated"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/terminal_conversation_tag_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_TAG_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal("actual tag CLI PTY", err, string(output))
	}
	if !strings.Contains(string(output), `"terminalRestored": true`) || !strings.Contains(string(output), `"internalProfileBlocked": true`) {
		t.Fatal("physical tag terminal restoration missing", string(output))
	}
}

func TestNativeTerminalConversationTagPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_TAG_PTY_HELPER") != "1" {
		t.Skip("owned isolated tag CLI helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "owned-tag-pty")); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		calls.Add(1)
		io.Copy(io.Discard, r.Body)
		http.Error(w, "unexpected tag model request", 500)
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Stderr = os.Stderr
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["USER_TYPE"] = "ant"
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	path := filepath.Join(config, "projects", projectpath.Sanitize(o.WorkDir), o.Arguments.SessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"type":"legacy-future","future":{"preserve":17}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(home, "tag-transcript-path.txt"), []byte(path), 0600)
	if err := os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	var env []string
	for key, value := range o.Environment {
		env = append(env, key+"="+value)
	}
	env = append(env, "TERM=xterm-256color")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	code, err := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, env)
	if code != 1 || err == nil || !strings.Contains(err.Error(), "internal schema host profile") {
		t.Fatal("internal public host profile guard changed", code, err)
	}
	if _, err = nativeTUIPresentationTerminalProbe(os.Stdin.Fd()); err != nil {
		t.Fatal(err)
	}
	after, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "{\"type\":\"legacy-future\",\"future\":{\"preserve\":17}}\n" || calls.Load() != 0 {
		t.Fatal("blocked profile changed prior transcript or opened model", err, string(data), calls.Load())
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("tag terminal state was not restored")
	}
	os.WriteFile(filepath.Join(home, "tag-terminal-restored"), []byte("true"), 0600)
	fmt.Fprintln(os.Stdout, "CLI_TAG_INTERNAL_PROFILE_BLOCKED")
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(home, "tag-pty-release")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("physical tag parent inspection missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
