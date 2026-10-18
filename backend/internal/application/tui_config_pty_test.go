package application

import (
	"context"
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

func TestActualCLIConfigSearchNestedModelRevertCommitAndNextWritesPTY(t *testing.T) {
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
	_ = os.WriteFile(filepath.Join(home, "disposable-config-pty"), []byte("owned"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_config_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_CONFIG_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Config PTY %v %.7000s", err, output)
	}
	if !strings.Contains(string(output), `"terminalRestored": true`) {
		t.Fatal(string(output))
	}
}
func TestNativeCLIConfigPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_CONFIG_PTY_HELPER") != "1" {
		t.Skip("owned PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-config-pty")); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	var sonnetFile, opusFile string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		index := requests.Add(1)
		want := "private-sonnet-config-model"
		if index > 2 {
			want = "private-opus-config-model"
		}
		if obj(body).String("model") != want {
			t.Error("actual Config next model", index, string(body))
		}
		switch index {
		case 1:
			response(w, "config-revert-write", "Write", map[string]any{"file_path": sonnetFile, "content": "PUBLIC_CONFIG_REVERT_SONNET"}, "")
		case 3:
			response(w, "config-commit-write", "Write", map[string]any{"file_path": opusFile, "content": "PUBLIC_CONFIG_COMMIT_OPUS"}, "")
		case 2:
			response(w, "config-revert-done", "", nil, "CONFIG_PUBLIC_REVERT_DONE_792")
		default:
			response(w, "config-commit-done", "", nil, "CONFIG_PUBLIC_COMMIT_DONE_792")
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrNotExist }
	o.Interactive = true
	o.Arguments.Model = "sonnet"
	o.Arguments.PermissionMode = "acceptEdits"
	o.Arguments.Tools = []string{"Read,Write"}
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		env["ANTHROPIC_DEFAULT_SONNET_MODEL"] = "private-sonnet-config-model"
		env["ANTHROPIC_DEFAULT_OPUS_MODEL"] = "private-opus-config-model"
		env["ANTHROPIC_DEFAULT_OPUS_MODEL_SUPPORTED_CAPABILITIES"] = "effort,max_effort"
	}
	sonnetFile = filepath.Join(o.WorkDir, "sonnet.txt")
	opusFile = filepath.Join(o.WorkDir, "opus.txt")
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.MkdirAll(config, 0700)
	settings := filepath.Join(config, "settings.json")
	_ = os.WriteFile(settings, []byte(`{"model":"initial-disk-model","effortLevel":"medium","futureUser":{"keep":true}}`), 0600)
	_ = os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}), 0600)
	_ = os.WriteFile(filepath.Join(home, "config-paths.json"), raw(map[string]any{"settings": settings, "sonnet": sonnetFile, "opus": opusFile}), 0600)
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
	_ = os.WriteFile(filepath.Join(home, "config-receipt.json"), raw(receipt), 0600)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err = os.Stat(filepath.Join(home, "config-pty-release")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("physical terminal inspection acknowledgement missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if requests.Load() != 4 || !reflect.DeepEqual(before, after) {
		t.Fatal(receipt)
	}
}
