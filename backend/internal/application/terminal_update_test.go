package application

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliinstallation"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"github.com/NanmiCoder/cc-haha/backend/testdata/gochildcoverage"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func terminalUpdatePrivateGoRuntime(t *testing.T) []byte {
	t.Helper()
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	binary := filepath.Join(directory, "private-release-bin")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, goTool, "build", "-tags", "native_process_fixture", "-ldflags", "-X main.version=2.3.5", "-o", binary, "./cmd/claude-haha")
	cmd.Dir = "../.."
	capturePublicGoCoverage(t, cmd)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("actual Go runtime %v %.6000s", err, output)
	}
	gochildcoverage.RegisterBuilt(t, cmd)
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func TestTerminalAutomaticUpdaterActualGoReleaseMinimumVersionFailureAndCancellation(t *testing.T) {
	data := terminalUpdatePrivateGoRuntime(t)
	digest := sha256.Sum256(data)
	name, err := cliinstallation.BinaryName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	var gets, downloads atomic.Int32
	var forceError, hold atomic.Bool
	started, cancelled := make(chan struct{}, 1), make(chan struct{}, 1)
	var releaseTag atomic.Value
	releaseTag.Store("v2.3.5")
	var peer *httptest.Server
	peer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if forceError.Load() {
			http.Error(w, "owned fixture unavailable", 503)
			return
		}
		if hold.Load() {
			started <- struct{}{}
			<-r.Context().Done()
			cancelled <- struct{}{}
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/releases"):
			gets.Add(1)
			_, _ = w.Write(raw([]any{map[string]any{"tag_name": releaseTag.Load(), "assets": []any{map[string]any{"name": cliinstallation.ManifestName, "browser_download_url": peer.URL + "/manifest"}, map[string]any{"name": name, "size": len(data), "browser_download_url": peer.URL + "/binary"}}}}))
		case r.URL.Path == "/manifest":
			_, _ = w.Write(raw(cliinstallation.Manifest{Version: strings.TrimPrefix(releaseTag.Load().(string), "v"), Runtime: "go", Assets: map[string]cliinstallation.ManifestAsset{name: {SHA256: fmt.Sprintf("%x", digest), Size: int64(len(data))}}}))
		case r.URL.Path == "/binary":
			downloads.Add(1)
			_, _ = w.Write(data)
		default:
			t.Error("unexpected update endpoint", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	o.Interactive = true
	o.Version = "2.3.4"
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		delete(env, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC")
		delete(env, "DISABLE_AUTOUPDATER")
		env["CC_HAHA_CLI_RELEASE_API"] = peer.URL
	}
	path := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json")
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	_ = os.WriteFile(path, []byte(`{"autoUpdatesChannel":"stable","minimumVersion":"2.3.6","future":true}`), 0600)
	seedTerminalReleaseWarmFixture(t, o)
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	var update protocolUpdateSnapshot
	emit := func(event tui.Event) error {
		if event.Kind == tui.TerminalUpdateChanged {
			update.phase = obj(event.Result).String("phase")
			update.latest = obj(event.Result).String("latest")
		}
		return nil
	}
	if err = s.CheckTerminalUpdate(context.Background(), emit); err != nil {
		t.Fatal(err)
	}
	if gets.Load() != 1 || downloads.Load() != 0 || update.latest != "2.3.5" || update.phase != "" {
		t.Fatal("real minimum version gate", gets.Load(), downloads.Load(), update)
	}
	if err = s.process.SetTerminalUpdateChannel(context.Background(), "latest"); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckTerminalUpdate(context.Background(), emit); err != nil {
		t.Fatal(err)
	}
	if downloads.Load() != 1 || update.phase != "success" {
		t.Fatal(downloads.Load(), update)
	}
	installer, err := cliinstallation.New(cliinstallation.Options{Home: o.Native.Home, Platform: runtime.GOOS, Architecture: runtime.GOARCH})
	if err != nil {
		t.Fatal(err)
	}
	installed := installer.Directories().Executable
	versionCmd := exec.Command(installed, "--version")
	versionCmd.Env = []string{"HOME=" + o.Native.Home, "USERPROFILE=" + o.Native.Home, "CLAUDE_CONFIG_DIR=" + o.Environment["CLAUDE_CONFIG_DIR"], "PATH=/usr/bin:/bin", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	capturePublicGoCoverage(t, versionCmd)
	version, err := versionCmd.CombinedOutput()
	if err != nil || !strings.Contains(string(version), "2.3.5") {
		t.Fatal("actual activated Go executable", err, string(version))
	}
	if err = s.CheckTerminalUpdate(context.Background(), emit); err != nil {
		t.Fatal(err)
	}
	if downloads.Load() != 1 || update.phase != "success" {
		t.Fatal("Source retains previous success on already installed check", downloads.Load(), update)
	}
	forceError.Store(true)
	if err = s.CheckTerminalUpdate(context.Background(), emit); err != nil {
		t.Fatal(err)
	}
	if update.phase != "install_failed" {
		t.Fatal("missing actual failure result", update)
	}
	afterFailure, err := os.ReadFile(installed)
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(afterFailure)) != fmt.Sprintf("%x", digest) {
		t.Fatal("failed check replaced executable", err)
	}
	forceError.Store(false)
	hold.Store(true)
	workerCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.CheckTerminalUpdate(workerCtx, emit) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("physical update request did not start")
	}
	duplicate := make(chan error, 1)
	go func() { duplicate <- s.CheckTerminalUpdate(context.Background(), emit) }()
	select {
	case err := <-duplicate:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-progress Source ref did not reject duplicate check")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("physical HTTP request outlived caller cancellation")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("update cancellation lost")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("update caller failed to join")
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer closeCancel()
	if err = s.Close(closeCtx); err != nil {
		t.Fatal("same Owner failed to join cancelled native update", err)
	}

}

type protocolUpdateSnapshot struct{ phase, latest string }
