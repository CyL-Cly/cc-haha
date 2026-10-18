package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
)

const configPermissionsPTYOriginal = `{"permissions":{"allow":["Read(owned/**)"],"future":{"keep":["nested",7]}},"futureUser":{"keep":true}}`

func TestActualCLIConfigEscapeRestoresMissingPermissionDefaultModePTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("physical Unix PTY; Windows ConPTY requires its own host")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("Python is required for this actual PTY regression", err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err = os.WriteFile(filepath.Join(home, "owned-config-permissions-pty"), []byte("owned fixture only\n"), 0600); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(home, "driver-output.txt")
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	// CommandContext's default Kill is intentionally not used. This direct
	// child gets at most one TERM, then a bounded wait; no process-group signal.
	cmd := exec.Command(python, "testdata/tui_config_permissions_restore_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CFFIXED_USER_HOME=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TMP=" + home, "TEMP=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "XDG_CACHE_HOME=" + filepath.Join(home, ".cache"), "XDG_DATA_HOME=" + filepath.Join(home, ".local", "share"), "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_CONFIG_PERMISSIONS_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	cmd.Stdout, cmd.Stderr = output, output
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	deadline := time.NewTimer(45 * time.Second)
	defer deadline.Stop()
	timedOut := false
	closed := false
	select {
	case err = <-done:
		closed = true
	case <-deadline.C:
		timedOut = true
		select {
		case err = <-done:
			closed = true
		default:
			if signalErr := cmd.Process.Signal(syscall.SIGTERM); signalErr != nil && !errors.Is(signalErr, os.ErrProcessDone) {
				t.Errorf("owned Python TERM failed: %v", signalErr)
			}
			grace := time.NewTimer(8 * time.Second)
			select {
			case err = <-done:
				closed = true
			case <-grace.C:
				t.Error("owned Python process did not close after its single TERM")
			}
			grace.Stop()
		}
	}
	data, readErr := os.ReadFile(outputPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if timedOut || !closed || err != nil {
		// Preserve Python's bounded final VT/settings/phase evidence in the
		// outer Go stdout before testing removes its owned temporary HOME.
		if len(data) > 32*1024 {
			data = data[len(data)-32*1024:]
		}
		t.Fatalf("actual Config Escape PTY failed: timeout=%v closed=%v err=%v\n%s", timedOut, closed, err, data)
	}
	var receipt struct {
		Qualified                bool `json:"qualified"`
		TerminalRestored         bool `json:"terminalRestored"`
		PhysicalTerminalRestored bool `json:"physicalTerminalRestored"`
		ModelPOSTRequests        int  `json:"modelPOSTRequests"`
		OtherRequests            int  `json:"otherRequests"`
		CleanupTERMAttempted     bool `json:"cleanupTERMAttempted"`
		ExitCode                 int  `json:"exitCode"`
	}
	if err = json.Unmarshal(data, &receipt); err != nil || !receipt.Qualified || !receipt.TerminalRestored || !receipt.PhysicalTerminalRestored || receipt.ModelPOSTRequests != 0 || receipt.OtherRequests != 0 || receipt.CleanupTERMAttempted || receipt.ExitCode != 0 {
		t.Fatal("actual PTY receipt did not meet the business/natural-close contract", receipt, err, string(data))
	}
}

func TestNativeCLIConfigPermissionsRestorePTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_CONFIG_PERMISSIONS_PTY_HELPER") != "1" {
		t.Skip("owned isolated Config permissions PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "owned-config-permissions-pty")); err != nil {
		t.Fatal(err)
	}
	var heads, posts, other atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			heads.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPost:
			posts.Add(1)
			t.Error("local Config keyboard actions unexpectedly started model POST")
			http.Error(w, "model POST forbidden", http.StatusInternalServerError)
		default:
			other.Add(1)
			t.Error("unexpected non-HEAD startup HTTP", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	o.Arguments.PermissionMode = "default"
	o.Arguments.Tools = []string{""}
	o.Arguments.SystemPrompt = "No inference in the owned Config Escape PTY regression"
	for key, value := range map[string]string{"USERPROFILE": o.Environment["HOME"], "CFFIXED_USER_HOME": o.Environment["HOME"], "TMPDIR": home, "TMP": home, "TEMP": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"), "XDG_CACHE_HOME": filepath.Join(home, ".cache"), "XDG_DATA_HOME": filepath.Join(home, ".local", "share"), "TERM": "xterm-256color"} {
		o.Environment[key] = value
		o.Native.Environment[key] = value
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(config, "settings.json")
	if err := os.WriteFile(settings, []byte(configPermissionsPTYOriginal), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"hasCompletedOnboarding": true, "futureGlobal": map[string]any{"keep": true}, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "permissions-paths.json"), raw(map[string]any{"settings": settings, "original": json.RawMessage(configPermissionsPTYOriginal)}), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 38*time.Second)
	defer cancel()
	var environment []string
	for key, value := range o.Environment {
		environment = append(environment, key+"="+value)
	}
	code, err := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, environment)
	if err != nil || code != 0 {
		t.Fatal("actual RunCommand did not exit naturally", code, err)
	}
	if _, err = nativeTUIPresentationTerminalProbe(os.Stdin.Fd()); err != nil {
		t.Fatal(err)
	}
	after, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	physical, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	var actual, original any
	if err = json.Unmarshal(physical, &actual); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(configPermissionsPTYOriginal), &original); err != nil {
		t.Fatal(err)
	}
	actualJSON, _ := json.Marshal(actual)
	originalJSON, _ := json.Marshal(original)
	preserved := bytes.Equal(actualJSON, originalJSON)
	restored := reflect.DeepEqual(before, after)
	receipt := map[string]any{"terminalRestored": restored, "unknownFieldsPreserved": preserved, "modelPOSTRequests": posts.Load(), "startupHEADRequests": heads.Load(), "otherRequests": other.Load(), "exitCode": code}
	if err = os.WriteFile(filepath.Join(home, "permissions-receipt.json"), raw(receipt), 0600); err != nil {
		t.Fatal(err)
	}
	ack := time.NewTimer(3 * time.Second)
	defer ack.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err = os.Stat(filepath.Join(home, "permissions-terminal-release")); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case <-ack.C:
			t.Fatal("Python did not acknowledge its independent termios inspection")
		case <-ticker.C:
		}
	}
	if !restored || !preserved || posts.Load() != 0 || other.Load() != 0 {
		t.Fatal("Config PTY violated terminal/settings/zero-model contract", receipt)
	}
}
