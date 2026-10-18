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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
)

type terminalUpdateHoldingWriter struct {
	http.ResponseWriter
	hold func()
}

func (w terminalUpdateHoldingWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	if err == nil && strings.Contains(string(data), `"type":"text_delta"`) {
		if flush, ok := w.ResponseWriter.(http.Flusher); ok {
			flush.Flush()
		}
		w.hold()
	}
	return n, err
}

func TestActualCLIPluginUpdateNowUnchangedConfigurationFailureReloadAndNextHumanPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY; ConPTY needs actual Windows host")
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
	if err = os.WriteFile(filepath.Join(home, "disposable-plugin-update-pty"), []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/tui_plugin_update_pty.py", binary, home)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=" + os.Getenv("PATH"), "TERM=xterm-256color", "TZ=UTC", "CC_HAHA_PLUGIN_UPDATE_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Update now CLI %v %.16000s", err, output)
	}
	t.Log(string(output))
}

func TestNativeCLIPluginUpdatePTYFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_PLUGIN_UPDATE_PTY_HELPER") != "1" {
		t.Skip("owned Update now PTY fixture")
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, "disposable-plugin-update-pty")); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	var invalidRequest atomic.Int32
	var secondRequestFlags atomic.Int32
	var secondRequestSnippet atomic.Value
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		n := requests.Add(1)
		if n == 2 {
			flags := int32(0)
			if strings.Contains(string(body), "UPDATE_SKILL_ONE_734") {
				flags |= 1
			}
			if strings.Contains(string(body), "UPDATE_SKILL_TWO_734") {
				flags |= 2
			}
			secondRequestFlags.Store(flags)
			if at := strings.Index(string(body), "UPDATE_SKILL_TWO_734"); at >= 0 {
				secondRequestSnippet.Store(string(body[max(0, at-220):min(len(body), at+260)]))
			}
		}
		switch n {
		case 1, 2:
			if !strings.Contains(string(body), "UPDATE_SKILL_ONE_734") || strings.Contains(string(body), "UPDATE_SKILL_TWO_734") {
				invalidRequest.Store(n)
				t.Error("update changed active Skill before explicit reload", n, "old", strings.Contains(string(body), "UPDATE_SKILL_ONE_734"), "new", strings.Contains(string(body), "UPDATE_SKILL_TWO_734"))
			}
		case 3:
			if !strings.Contains(string(body), "UPDATE_SKILL_TWO_734") {
				invalidRequest.Store(n)
				t.Error("new disk Skill never reached same Harness after reload", "old", strings.Contains(string(body), "UPDATE_SKILL_ONE_734"), "new", strings.Contains(string(body), "UPDATE_SKILL_TWO_734"))
			}
		case 4:
		default:
			t.Error("local Update now escaped to model", n)
		}
		markers := []string{"", "UPDATE_INITIAL_DONE_734", "UPDATE_BEFORE_RELOAD_DONE_734", "UPDATE_NEW_SKILL_DONE_734", "UPDATE_NEXT_HUMAN_DONE_734"}
		marker := "UNEXPECTED_MODEL"
		if int(n) < len(markers) {
			marker = markers[n]
		}
		var output http.ResponseWriter = w
		if n == 2 {
			output = terminalUpdateHoldingWriter{ResponseWriter: w, hold: func() {
				for {
					if _, err := os.Stat(filepath.Join(home, "update-held-model-release")); err == nil {
						return
					}
					select {
					case <-r.Context().Done():
						return
					case <-time.After(10 * time.Millisecond):
					}
				}
			}}
		}
		response(output, "update-actual-next", "", nil, marker)
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Stderr = os.Stderr
	prepareTerminalBulkFixture(t, o.WorkDir, o.Environment["CLAUDE_CONFIG_DIR"])
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	plugin := filepath.Join(o.WorkDir, "market", "plugins", "zeta")
	write(filepath.Join(plugin, "skills", "run", "SKILL.md"), []byte("---\ndescription: Update actual Skill\n---\nUPDATE_SKILL_ONE_734"))
	write(filepath.Join(config, ".claude.json"), raw(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}}))
	write(filepath.Join(home, "update-paths.json"), raw(map[string]string{"config": config, "plugin": plugin}))
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
	_, _ = nativeTUIPresentationTerminalProbe(os.Stdin.Fd())
	after, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(config, "plugins", "installed_plugins.json"))
	if err != nil {
		t.Fatal(err)
	}
	installs := array(obj(obj(data).Get("plugins")).Get("zeta@mkt"))
	settings, err := os.ReadFile(filepath.Join(config, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	valid := len(installs) == 1 && installs[0].String("version") == "2.0.0" && obj(obj(obj(settings).Get("pluginConfigs")).Get("zeta@mkt")).Get("options") != nil && string(obj(obj(settings).Get("futureUser")).Get("keep")) == "true"
	if valid {
		valid = obj(obj(obj(obj(settings).Get("pluginConfigs")).Get("zeta@mkt")).Get("options")).String("region") == "Updated region"
	}
	orphan, orphanErr := os.ReadFile(filepath.Join(config, "plugins", "cache", "mkt", "zeta", "1.0.0", ".orphaned_at"))
	valid = valid && orphanErr == nil && len(orphan) > 0
	receipt := map[string]any{"requests": requests.Load(), "invalidRequest": invalidRequest.Load(), "secondRequestFlags": secondRequestFlags.Load(), "exitCode": code, "actualDiskVersionOptionsAndOrphan": valid, "terminalRestored": reflect.DeepEqual(before, after)}
	if snippet := secondRequestSnippet.Load(); snippet != nil {
		receipt["prematureSnippet"] = snippet
	}
	write(filepath.Join(home, "update-receipt.json"), raw(receipt))
	for {
		if _, err = os.Stat(filepath.Join(home, "update-termios-release")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("actual slave termios receipt not released")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if requests.Load() != 4 || !valid || !reflect.DeepEqual(before, after) {
		t.Fatal(string(raw(receipt)), string(settings), string(data))
	}
}
