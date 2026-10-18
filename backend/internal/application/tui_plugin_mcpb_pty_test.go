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
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
)

func TestActualCLIPluginMCPBConfigureCancelRemountStorageToolAndNextHumanPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY; actual Windows host separate")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("private Python PTY unavailable")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	_ = os.WriteFile(filepath.Join(home, "disposable-plugin-mcpb-pty"), []byte("owned"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_plugin_mcpb_pty.py", binary, home)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=" + os.Getenv("PATH"), "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_PLUGIN_MCPB_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual public MCPB PTY %v %.9000s", err, output)
	}
	t.Log(string(output))
}
func TestNativeCLIPluginMCPBPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_PLUGIN_MCPB_PTY_HELPER") != "1" {
		t.Skip("private actual PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-plugin-mcpb-pty")); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		n := requests.Add(1)
		if n == 1 {
			name := ""
			for _, tool := range array(obj(body).Get("tools")) {
				if strings.HasSuffix(tool.String("name"), "__deliver") {
					name = tool.String("name")
				}
			}
			if name == "" {
				t.Error("public MCPB saved config absent from next actual catalog")
			}
			response(w, "public-bundle-tool", name, map[string]any{}, "")
		} else if n == 2 {
			if !strings.Contains(string(body), "ACTUAL_BUNDLE_PHYSICAL_DELIVERY") {
				t.Error("public MCPB actual tool result absent")
			}
			response(w, "public-bundle-done", "", nil, "PUBLIC_MCPB_ACTUAL_FILE_DONE_527")
		} else {
			response(w, "public-bundle-next", "", nil, "PUBLIC_MCPB_NEXT_HUMAN_DONE_527")
		}
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Stderr = os.Stderr
	target := prepareTerminalMCPBFixture(t, o)
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}), 0600)
	_ = os.WriteFile(filepath.Join(home, "mcpb-paths.json"), raw(map[string]string{"config": config, "target": target}), 0600)
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
	_, _ = nativeTUIPresentationTerminalProbe(os.Stdin.Fd())
	after, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	piddata, err := os.ReadFile(filepath.Join(o.WorkDir, "bundle-peer.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(piddata))
	if err != nil {
		t.Fatal(err)
	}
	process, findErr := os.FindProcess(pid)
	joined := findErr != nil || process.Signal(syscall.Signal(0)) != nil
	receipt := map[string]any{"requests": requests.Load(), "exitCode": code, "terminalRestored": reflect.DeepEqual(before, after), "actualBundleFile": string(data) == "PUBLIC_BUNDLE_FINAL|PRIVATE_BUNDLE_PUBLIC", "nativeMCPChildJoined": joined}
	_ = os.WriteFile(filepath.Join(home, "mcpb-receipt.json"), raw(receipt), 0600)
	for {
		if _, err := os.Stat(filepath.Join(home, "mcpb-termios-release")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("termios proof never released helper")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if requests.Load() != 3 || !joined || !reflect.DeepEqual(before, after) {
		t.Fatal(receipt)
	}
}
