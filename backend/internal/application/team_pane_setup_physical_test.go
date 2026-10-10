package application

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualITermSetupTerminalChooseTmuxStartsGoPaneWorkerWritesAndCloseJoins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("actual private tmux process")
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "actual-team-cli")
	build := exec.CommandContext(ctx, "go", "build", "-p=4", "-o", binary, "../../cmd/claude-haha")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	private, err := os.MkdirTemp("/tmp", "team-it2-ui-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(private)
	_ = os.Mkdir(filepath.Join(private, "tmux"), 0700)
	_ = os.Mkdir(filepath.Join(private, "bin"), 0700)
	for _, name := range []string{"which", "it2"} {
		_ = os.WriteFile(filepath.Join(private, "bin", name), []byte("#!/bin/sh\nexit 1\n"), 0755)
	}
	target := ""
	var parents, children atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "# Agent Teammate Communication") {
			switch children.Add(1) {
			case 1:
				response(w, "setup-worker-write", "Write", map[string]string{"file_path": target, "content": "ACTUAL_ITERM_SETUP_TMUX_WORKER"}, "")
			default:
				response(w, "setup-worker-finish", "", nil, "physical setup teammate idle")
			}
			return
		}
		switch parents.Add(1) {
		case 1:
			response(w, "setup-team-create", "TeamCreate", map[string]string{"team_name": "actual-iterm-ui"}, "")
		case 2:
			response(w, "setup-team-agent", "Agent", map[string]string{"name": "worker", "prompt": "write a real private artifact after teammate setup", "description": "actual setup worker"}, "")
		default:
			response(w, "setup-parent-ready", "", nil, "ACTUAL_PARENT_SETUP_READY")
		}
	}))
	defer provider.Close()
	options := nativeSessionOptions(t, provider.URL)
	options.Interactive = true
	options.Arguments.Tools = []string{"TeamCreate,TeamDelete,Agent,Write,Read,SendMessage"}
	options.Arguments.TeammateMode = "tmux"
	for _, env := range []map[string]string{options.Environment, options.Native.Environment} {
		env["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		env["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"] = "1"
		env["TERM_PROGRAM"] = "iTerm.app"
		env["PATH"] = filepath.Join(private, "bin") + ":" + filepath.Dir(tmux) + ":/usr/bin:/bin"
		env["TMUX_TMPDIR"] = filepath.Join(private, "tmux")
		env["CLAUDE_CODE_TEAMMATE_COMMAND"] = binary
		delete(env, "TMUX")
	}
	config := options.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.Mkdir(config, 0700)
	_ = os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"agentTeamsEnabled":true,"enabledPlugins":{"sample@mkt":false},"alwaysThinkingEnabled":false,"permissions":{"defaultMode":"bypassPermissions"}}`), 0600)
	_ = os.WriteFile(filepath.Join(config, ".claude.json"), []byte(`{"future":{"keep":true},"preferTmuxOverIterm2":false}`), 0600)
	target = filepath.Join(options.WorkDir, "setup-worker.txt")
	session, err := NewSession(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close(context.Background())
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	output := &teammateHarnessOutput{}
	done := make(chan error, 1)
	go func() {
		done <- tui.Run(ctx, session, tui.Options{Input: reader, Output: output, InitialPrompt: "create the actual team and spawn a worker", Title: "actual iTerm setup", Width: 110, Height: 40, Environment: []string{"TERM=xterm-256color"}})
	}()
	wait := func(fn func() bool, label string) {
		t.Helper()
		for !fn() {
			select {
			case err := <-done:
				t.Fatal(label, "terminal returned", err, output.Text())
			case <-ctx.Done():
				t.Fatal(label, ctx.Err(), output.Text())
			case <-time.After(15 * time.Millisecond):
			}
		}
	}
	wait(func() bool {
		return strings.Contains(output.Text(), "iTerm2 Split Pane Setup") && strings.Contains(output.Text(), "Use tmux instead")
	}, "original setup choice")
	if parents.Load() != 2 || children.Load() != 0 {
		t.Fatal("spawn proceeded before setup choice", parents.Load(), children.Load())
	}
	if _, err = writer.Write([]byte("\x1b[B\r")); err != nil {
		t.Fatal(err)
	}
	wait(func() bool {
		data, _ := os.ReadFile(target)
		return string(data) == "ACTUAL_ITERM_SETUP_TMUX_WORKER" && strings.Contains(output.Text(), "ACTUAL_PARENT_SETUP_READY")
	}, "actual setup pane CLI artifact")
	saved, _ := os.ReadFile(filepath.Join(config, ".claude.json"))
	if !strings.Contains(string(saved), `"preferTmuxOverIterm2": true`) && !strings.Contains(string(saved), `"preferTmuxOverIterm2":true`) {
		t.Fatal("Source tmux preference not saved", string(saved))
	}
	if !strings.Contains(string(saved), `"keep"`) {
		t.Fatal("old unknown state dropped", string(saved))
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("setup TUI did not join")
	}
	if err = session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	list := exec.Command(tmux, "-L", "claude-swarm-"+strconv.Itoa(os.Getpid()), "list-panes", "-a")
	list.Env = []string{"PATH=" + filepath.Dir(tmux) + ":/usr/bin:/bin", "TMUX_TMPDIR=" + filepath.Join(private, "tmux")}
	if out, err := list.CombinedOutput(); err == nil && len(out) > 0 {
		t.Fatal("setup pane survived parent close", string(out))
	}
}
