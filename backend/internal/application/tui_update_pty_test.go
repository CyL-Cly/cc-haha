package application

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliinstallation"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
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

func TestActualCLIAutomaticUpdaterStartupChannelMinimumAndStopPTY(t *testing.T) {
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
	_ = os.WriteFile(filepath.Join(home, "disposable-update-pty"), []byte("owned"), 0600)
	releaseBinary := filepath.Join(home, "private-go-release.bin")
	if err := os.WriteFile(releaseBinary, terminalUpdatePrivateGoRuntime(t), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_update_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_UPDATE_PTY_HELPER=1", "CC_HAHA_UPDATE_PTY_RELEASE_BINARY=" + releaseBinary}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		journal, _ := os.ReadFile(filepath.Join(home, "update-terminal.txt"))
		t.Logf("Actual updater failure journal:\n%s", journal)
		t.Fatalf("actual native updater PTY %v %.7000s", err, output)
	}
	if !strings.Contains(string(output), `"terminalRestored": true`) {
		t.Fatal(string(output))
	}
}
func TestNativeCLIUpdaterPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_UPDATE_PTY_HELPER") != "1" {
		t.Skip("owned PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-update-pty")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, "private-go-release.bin"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	name, err := cliinstallation.BinaryName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	var gets, downloads, modelRequests atomic.Int32
	var peer *httptest.Server
	peer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := os.Stat(filepath.Join(home, "hold-update")); err == nil && strings.HasPrefix(r.URL.Path, "/releases") {
			_ = os.WriteFile(filepath.Join(home, "update-started"), []byte("physical"), 0600)
			<-r.Context().Done()
			_ = os.WriteFile(filepath.Join(home, "update-cancelled"), []byte("joined"), 0600)
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/releases"):
			count := gets.Add(1)
			_ = os.WriteFile(filepath.Join(home, "release-check-count"), []byte(fmt.Sprint(count)), 0600)
			assets := []any{map[string]any{"name": cliinstallation.ManifestName, "browser_download_url": peer.URL + "/manifest"}, map[string]any{"name": name, "size": len(data), "browser_download_url": peer.URL + "/binary"}}
			_, _ = w.Write(raw([]any{map[string]any{"tag_name": "v2.3.5", "prerelease": true, "assets": assets}, map[string]any{"tag_name": "v2.3.3", "assets": assets}}))
		case r.URL.Path == "/manifest":
			_, _ = w.Write(raw(cliinstallation.Manifest{Version: "2.3.5", Runtime: "go", Assets: map[string]cliinstallation.ManifestAsset{name: {SHA256: fmt.Sprintf("%x", digest), Size: int64(len(data))}}}))
		case r.URL.Path == "/binary":
			downloads.Add(1)
			_, _ = w.Write(data)
		default:
			modelRequests.Add(1)
			t.Error("unexpected provider request", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	o.Interactive = true
	o.Version = "2.3.4"
	o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrNotExist }
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		delete(env, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC")
		delete(env, "DISABLE_AUTOUPDATER")
		env["CC_HAHA_CLI_RELEASE_API"] = peer.URL
	}
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.MkdirAll(config, 0700)
	settings := filepath.Join(config, "settings.json")
	_ = os.WriteFile(settings, []byte(`{"autoUpdatesChannel":"latest","futureUser":{"keep":true}}`), 0600)
	_ = os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}), 0600)
	seedTerminalReleaseWarmFixture(t, o)
	installer, err := cliinstallation.New(cliinstallation.Options{Home: o.Native.Home, Platform: runtime.GOOS, Architecture: runtime.GOARCH})
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(home, "update-paths.json"), raw(map[string]any{"settings": settings, "installed": installer.Directories().Executable}), 0600)
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
	receipt := map[string]any{"terminalRestored": reflect.DeepEqual(before, after), "gets": gets.Load(), "downloads": downloads.Load(), "modelRequests": modelRequests.Load(), "exitCode": code}
	_ = os.WriteFile(filepath.Join(home, "update-receipt.json"), raw(receipt), 0600)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err = os.Stat(filepath.Join(home, "update-pty-release")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("physical terminal inspection acknowledgement missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if gets.Load() != 2 || downloads.Load() != 1 || modelRequests.Load() != 0 || !reflect.DeepEqual(before, after) {
		t.Fatal(receipt)
	}
}
