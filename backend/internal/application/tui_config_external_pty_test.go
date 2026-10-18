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

func TestActualCLIConfigExternalIncludesApproveClearDisableAndNextWritePTY(t *testing.T) {
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
	_ = os.WriteFile(filepath.Join(home, "disposable-config-external-pty"), []byte("owned"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_config_external_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_CONFIG_EXTERNAL_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual external Config PTY %v %.9000s", err, output)
	}
	if !strings.Contains(string(output), `"terminalRestored": true`) || !strings.Contains(string(output), `"sameHarnessWrite": true`) {
		t.Fatal(string(output))
	}
}

func TestNativeCLIConfigExternalPTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_CONFIG_EXTERNAL_PTY_HELPER") != "1" {
		t.Skip("owned PTY helper")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-config-external-pty")); err != nil {
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
		messages := string(obj(body).Get("messages"))
		project, external := strings.Contains(messages, "PUBLIC_EXTERNAL_PROJECT_INSTRUCTIONS_572"), strings.Contains(messages, "PUBLIC_EXTERNAL_IMPORTED_INSTRUCTIONS_572")
		if !project || external != (index == 3) {
			t.Errorf("actual query %d project=%v external=%v expected external=%v", index, project, external, index == 3)
		}
		_ = os.WriteFile(filepath.Join(home, fmt.Sprintf("external-request-%d.json", index)), raw(map[string]any{"project": project, "external": external}), 0600)
		switch index {
		case 1:
			response(w, "external-first", "", nil, "EXTERNAL_FIRST_DONE_572")
		case 2:
			response(w, "external-cached", "", nil, "EXTERNAL_CACHED_DONE_572")
		case 3:
			if strings.Contains(messages, "EXTERNAL_FIRST_DONE_572") || strings.Contains(messages, "EXTERNAL_CACHED_DONE_572") {
				t.Error("clear preserved original old conversation at the actual model boundary")
			}
			response(w, "external-approved", "", nil, "EXTERNAL_APPROVED_DONE_572")
		case 4:
			response(w, "external-write", "Write", map[string]any{"file_path": artifact, "content": "EXTERNAL_CONFIG_ACTUAL_NEXT_WRITE_572"}, "")
		default:
			response(w, "external-disabled", "", nil, "EXTERNAL_DISABLED_WRITE_DONE_572")
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Arguments.SystemPrompt, o.Interactive = "", true
	external := filepath.Join(filepath.Dir(o.WorkDir), "outside-claude.md")
	artifact = filepath.Join(o.WorkDir, "external-next-write.txt")
	_ = os.WriteFile(external, []byte("PUBLIC_EXTERNAL_IMPORTED_INSTRUCTIONS_572"), 0600)
	_ = os.WriteFile(filepath.Join(o.WorkDir, "CLAUDE.md"), []byte("PUBLIC_EXTERNAL_PROJECT_INSTRUCTIONS_572\n@"+external+"\n"), 0600)
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.MkdirAll(config, 0700)
	global := filepath.Join(config, ".claude.json")
	_ = os.WriteFile(global, raw(map[string]any{"hasCompletedOnboarding": true, "futureGlobal": map[string]any{"keep": true}, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true, "futureProject": map[string]any{"keep": true}}}}), 0600)
	_ = os.WriteFile(filepath.Join(home, "external-paths.json"), raw(map[string]any{"global": global, "work": o.WorkDir, "external": external, "artifact": artifact}), 0600)
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 32*time.Second)
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
	globalData, err := os.ReadFile(global)
	if err != nil {
		t.Fatal(err)
	}
	project := obj(obj(globalData).Get("projects"))
	project = obj(project.Get(o.WorkDir))
	data, err := os.ReadFile(artifact)
	receipt := map[string]any{"terminalRestored": reflect.DeepEqual(before, after), "requests": requests.Load(), "exitCode": code, "sameHarnessWrite": err == nil && string(data) == "EXTERNAL_CONFIG_ACTUAL_NEXT_WRITE_572", "unknownFieldsPreserved": string(obj(obj(globalData).Get("futureGlobal")).Get("keep")) == "true" && string(obj(project.Get("futureProject")).Get("keep")) == "true", "externalDisabled": string(project.Get("hasClaudeMdExternalIncludesApproved")) == "false" && string(project.Get("hasClaudeMdExternalIncludesWarningShown")) == "true"}
	_ = os.WriteFile(filepath.Join(home, "external-receipt.json"), raw(receipt), 0600)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(home, "external-release")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("physical terminal inspection acknowledgement missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if requests.Load() != 5 || !receipt["terminalRestored"].(bool) || !receipt["sameHarnessWrite"].(bool) || !receipt["unknownFieldsPreserved"].(bool) || !receipt["externalDisabled"].(bool) {
		t.Fatal(receipt)
	}
}
