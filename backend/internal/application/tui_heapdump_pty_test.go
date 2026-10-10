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
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/heapdump"
	"github.com/charmbracelet/x/term"
	"github.com/google/pprof/profile"
)

func TestActualCLIHiddenHeapDumpNativeFilesAndNextHumanPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY; ConPTY separate required")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("native Python PTY required")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err = os.WriteFile(filepath.Join(home, "disposable-heap-pty"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_heapdump_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_TUI_HEAP_HELPER=1"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual native heap PTY %v %.7000s", err, out)
	}
	if !strings.Contains(string(out), `"terminalRestored": true`) {
		t.Fatal(string(out))
	}
}
func TestNativeTUIHeapDumpFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_TUI_HEAP_HELPER") != "1" {
		t.Skip("owned private PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-heap-pty")); err != nil {
		t.Fatal(err)
	}
	var posts atomic.Int32
	var history atomic.Bool
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		posts.Add(1)
		history.Store(strings.Contains(string(body), ".heap.pprof") && strings.Contains(string(body), "<local-command-stdout>"))
		response(w, "release-next", "", nil, "HEAP_ACTUAL_NEXT_HUMAN_834")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Stderr = os.Stderr
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
	paths, _ := filepath.Glob(filepath.Join(o.Environment["HOME"], "Desktop", "*.heap.pprof"))
	if len(paths) != 1 {
		t.Fatal("actual native heap missing", paths)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = profile.ParseData(data); err != nil {
		t.Fatal("invalid real heap profile", err)
	}
	sid := strings.TrimSuffix(filepath.Base(paths[0]), ".heap.pprof")
	raw, err := os.ReadFile(filepath.Join(o.Environment["HOME"], "Desktop", sid+"-diagnostics.json"))
	if err != nil {
		t.Fatal(err)
	}
	var diag heapdump.Diagnostics
	_ = json.Unmarshal(raw, &diag)
	if diag.SessionID != sid || diag.Runtime != "go" || diag.MemoryUsage.HeapAlloc == 0 {
		t.Fatal(diag)
	}
	sessions, _ := filepath.Glob(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects", "*", sid+".jsonl"))
	canonical := false
	if len(sessions) == 1 {
		data, _ := os.ReadFile(sessions[0])
		canonical = strings.Contains(string(data), "<command-name>/heapdump") && strings.Contains(string(data), "HEAP_ACTUAL_NEXT_HUMAN_834")
	}
	receipt, _ := json.Marshal(map[string]any{"requests": posts.Load(), "actualLocalHistory": history.Load(), "canonical": canonical, "terminalRestored": reflect.DeepEqual(before, after)})
	if err = os.WriteFile(filepath.Join(home, "heap-receipt.json"), receipt, 0600); err != nil {
		t.Fatal(err)
	}
}
