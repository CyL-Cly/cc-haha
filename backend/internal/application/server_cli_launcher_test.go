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
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/clilauncher"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestDesktopBootAndHTTPInstallSameBundledCLILauncherWithoutModel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable wrapper; Windows runner is covered by clilauncher source and runner tests")
	}
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	home := o.Environment["HOME"]
	bundle := filepath.Join(home, "private bundle's path")
	if err := os.MkdirAll(bundle, 0700); err != nil {
		t.Fatal(err)
	}
	sidecar := filepath.Join(bundle, "claude-sidecar-go-test")
	if err := os.WriteFile(sidecar, []byte("#!/bin/sh\nprintf '%s\\n' \"$CLAUDE_CONFIG_DIR\" \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	o.Environment["CLAUDE_CLI_PATH"] = sidecar
	o.Environment["SHELL"] = "/bin/zsh"
	o.Environment["PATH"] = "/usr/bin:/bin"
	profile := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(profile, []byte("export EXISTING_SETTING=preserve\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return newIsolatedProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	// Observe the actual asynchronous constructor producer before any status GET.
	launcher := filepath.Join(home, ".local", "bin", "claude-haha")
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err = os.Stat(launcher); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("desktop boot did not prepare bundled launcher", err)
		}
		time.Sleep(time.Millisecond)
	}
	host := httptest.NewServer(s)
	defer host.Close()
	response, err := host.Client().Get(host.URL + "/api/settings/cli-launcher")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	var status clilauncher.Status
	if err != nil || response.StatusCode != http.StatusOK || json.Unmarshal(data, &status) != nil || !status.Installed || !status.AvailableInNewTerminals || !status.NeedsTerminalRestart || status.LastError != nil || status.LauncherPath != launcher {
		t.Fatalf("actual launcher API: status=%d body=%s error=%v", response.StatusCode, data, err)
	}
	cmd := exec.Command(status.LauncherPath, "--version")
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "CLAUDE_CONFIG_DIR=incorrect"}
	output, err := cmd.CombinedOutput()
	expected := strings.Join([]string{o.Environment["CLAUDE_CONFIG_DIR"], "cli", "--app-root", bundle, "--version", ""}, "\n")
	if err != nil || string(output) != expected {
		t.Fatalf("status advertised a different CLI/config: %v got=%q want=%q", err, output, expected)
	}
	if err = s.Close(context.Background()); err != nil {
		t.Fatal("desktop launcher shutdown", err)
	}
	profileData, err := os.ReadFile(profile)
	if err != nil || !strings.Contains(string(profileData), "export EXISTING_SETTING=preserve") || strings.Count(string(profileData), "# >>> Claude Code Haha PATH >>>") != 1 {
		t.Fatal("constructor and status GET duplicated or replaced private shell profile", err, string(profileData))
	}
}
