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

	"github.com/charmbracelet/x/term"
)

func TestActualCLIConfigStyleLanguageSettingsTabsAndFreshSessionPTY(t *testing.T) {
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
	_ = os.WriteFile(filepath.Join(home, "disposable-config-content-pty"), []byte("owned"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_config_content_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_CONFIG_CONTENT_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Config content PTY %v %.7000s", err, output)
	}
	if !strings.Contains(string(output), `"terminalRestored": true`) {
		t.Fatal(string(output))
	}
}

func TestNativeCLIConfigContentPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_CONFIG_CONTENT_PTY_HELPER") != "1" {
		t.Skip("owned PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-config-content-pty")); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	var artifact string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		index := requests.Add(1)
		system := string(obj(body).Get("system"))
		style, language := strings.Contains(system, "PUBLIC_CONTENT_STYLE_PROMPT_389"), strings.Contains(system, "Always respond in 日本語")
		if index <= 3 && (!style || !language) || index == 4 && (style || strings.Contains(system, "# Language")) {
			t.Errorf("actual model request %d style=%v language=%v", index, style, language)
		}
		_ = os.WriteFile(filepath.Join(home, fmt.Sprintf("content-request-%d.json", index)), raw(map[string]any{"style": style, "language": language}), 0600)
		switch index {
		case 1:
			response(w, "config-content-first", "", nil, "CONFIG_CONTENT_FIRST_DONE_389")
		case 2:
			response(w, "config-content-write", "Write", map[string]any{"file_path": artifact, "content": "PUBLIC_SETTINGS_TABS_ACTUAL_WRITE"}, "")
		case 3:
			response(w, "config-content-cached", "", nil, "CONFIG_CONTENT_CACHED_DONE_389")
		default:
			response(w, "config-content-fresh", "", nil, "CONFIG_CONTENT_FRESH_DONE_389")
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Interactive = true
	o.Arguments.SystemPrompt = ""
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.MkdirAll(filepath.Join(config, "output-styles"), 0700)
	_ = os.WriteFile(filepath.Join(config, "output-styles", "owned.md"), []byte("---\nname: Fixture Style\ndescription: Public native custom style description\n---\nPUBLIC_CONTENT_STYLE_PROMPT_389"), 0600)
	settings := filepath.Join(config, "settings.json")
	local := filepath.Join(o.WorkDir, ".claude", "settings.local.json")
	_ = os.MkdirAll(filepath.Dir(local), 0700)
	_ = os.WriteFile(settings, []byte(`{"futureUser":{"keep":true}}`), 0600)
	_ = os.WriteFile(local, []byte(`{"futureLocal":{"keep":true}}`), 0600)
	global := filepath.Join(config, ".claude.json")
	_ = os.WriteFile(global, raw(map[string]any{"hasCompletedOnboarding": true, "futureGlobal": map[string]any{"keep": true}, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}), 0600)
	artifact = filepath.Join(o.WorkDir, "tabs-write.txt")
	_ = os.WriteFile(filepath.Join(home, "content-paths.json"), raw(map[string]any{"settings": settings, "local": local, "global": global, "artifact": artifact}), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 42*time.Second)
	defer cancel()
	for phase := 1; phase <= 2; phase++ {
		before, err := term.GetState(os.Stdin.Fd())
		if err != nil {
			t.Fatal(err)
		}
		if phase == 2 {
			o.Arguments.SessionID = "e03a12f7-78f5-4169-8d1d-53d32fd43e84"
			_, _ = fmt.Fprintln(os.Stdout, "CONFIG_CONTENT_SECOND_CLI_START_389")
		}
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
		userData, userErr := os.ReadFile(settings)
		localData, localErr := os.ReadFile(local)
		globalData, globalErr := os.ReadFile(global)
		preserved := userErr == nil && localErr == nil && globalErr == nil && string(obj(obj(userData).Get("futureUser")).Get("keep")) == "true" && string(obj(obj(localData).Get("futureLocal")).Get("keep")) == "true" && string(obj(obj(globalData).Get("futureGlobal")).Get("keep")) == "true"
		receipt["unknownFieldsPreserved"] = preserved
		_ = os.WriteFile(filepath.Join(home, fmt.Sprintf("content-receipt-%d.json", phase)), raw(receipt), 0600)
		deadline := time.Now().Add(3 * time.Second)
		for {
			if _, err = os.Stat(filepath.Join(home, fmt.Sprintf("content-release-%d", phase))); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("physical terminal inspection acknowledgement missing", phase)
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !reflect.DeepEqual(before, after) || !preserved {
			t.Fatal(receipt)
		}
	}
	if requests.Load() != 4 {
		t.Fatal("panels crossed model boundary", requests.Load())
	}
}
