package clibootstrap

import (
	"context"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"io"
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

func TestActualPaneProducerStartsGoCLIBinaryFromPrivateTmuxInitialInboxWritesAndParentCloseKills(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("real private tmux is POSIX")
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "claude-haha-go")
	build := exec.CommandContext(ctx, "go", "build", "-p=4", "-o", binary, "../../cmd/claude-haha")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatal("isolated actual CLI build", err, string(out))
	}
	private, err := os.MkdirTemp("/tmp", "team-cli-pane-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(private)
	socketDir := filepath.Join(private, "tmux")
	_ = os.Mkdir(socketDir, 0700)
	var posts atomic.Int32
	artifact := ""
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch posts.Add(1) {
		case 1:
			if !strings.Contains(string(body), "ACTUAL_PRIVATE_PANE_INITIAL") || !strings.Contains(string(body), "# Agent Teammate Communication") {
				t.Error("physical CLI MAIN prompt missing", string(body))
			}
			emitNativeTaskTool(w, "pane-actual-write", "Write", map[string]any{"file_path": artifact, "content": "ACTUAL_GO_CLI_FROM_PRIVATE_TMUX"})
		case 2:
			emitPlanFixture(w, 2, "", "actual CLI teammate complete")
		default:
			t.Error("unexpected child model requests", posts.Load())
			http.Error(w, "unexpected", 500)
		}
	}))
	defer provider.Close()
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
		n.Environment["PATH"] = filepath.Dir(tmux) + ":/usr/bin:/bin"
		n.Environment["TMUX_TMPDIR"] = socketDir
		n.Environment["TERM"] = "xterm-256color"
		delete(n.Environment, "TMUX")
		delete(n.Environment, "ITERM_SESSION_ID")
		delete(n.Environment, "TERM_PROGRAM")
		n.Environment["ANTHROPIC_BASE_URL"] = provider.URL
		n.Environment["CLAUDE_CODE_TEAMMATE_COMMAND"] = binary
		n.Environment["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"] = "1"
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
	}, func(o *MainPrintOptions) {
		o.Interactive = true
		o.Arguments.TeammateMode = "tmux"
		o.Arguments.Tools = []string{"TeamCreate,TeamDelete,Agent,Read,Write,SendMessage"}
	})
	artifact = filepath.Join(m.workingDirectory(), "actual-pane-process-proof.txt")
	// tmux's default shell inherits only this private HOME/config/fixture provider.
	// Its startup state is isolated before Source sends the explicit env command.
	if err = os.WriteFile(filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"enabledPlugins":{"sample@mkt":false},"alwaysThinkingEnabled":false,"permissions":{"defaultMode":"bypassPermissions"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	callActualTeamTool(t, m, "TeamCreate", `{"team_name":"physical-cli-panes"}`)
	var result protocol.JSONObject
	err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		future, err := m.stageNativeTeamAgent(ctx, scope, "physical-cli-panes", objMain(rawMain(map[string]string{"name": "worker", "prompt": "ACTUAL_PRIVATE_PANE_INITIAL", "subagent_type": "general-purpose"})), queryloop.JSONToolCallContext{})
		if err != nil {
			return err
		}
		value, err := future.Await(scope)
		if err == nil {
			_ = result.UnmarshalJSON(value.Data)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.String("tmux_pane_id") == "" || result.String("tmux_session_name") != "claude-swarm" {
		t.Fatal("actual producer did not create pane", string(rawMain(result)))
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(artifact); err == nil && string(data) == "ACTUAL_GO_CLI_FROM_PRIVATE_TMUX" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if data, err := os.ReadFile(artifact); err != nil || string(data) != "ACTUAL_GO_CLI_FROM_PRIVATE_TMUX" {
		args := []string{"-L", fmt.Sprintf("claude-swarm-%d", m.options.PID), "capture-pane", "-t", result.String("tmux_pane_id"), "-p", "-S", "-200"}
		command := exec.CommandContext(ctx, tmux, args...)
		command.Env = nativeAgentProcessEnvironment(m.options.Native)
		out, _ := command.CombinedOutput()
		t.Fatal("real Go CLI pane did not write artifact", string(data), err, string(out))
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	list := exec.CommandContext(ctx, tmux, "-L", fmt.Sprintf("claude-swarm-%d", m.options.PID), "list-panes", "-a", "-F", "#{pane_id}")
	list.Env = nativeAgentProcessEnvironment(m.options.Native)
	out, _ := list.CombinedOutput()
	if strings.Contains(string(out), result.String("tmux_pane_id")) {
		t.Fatal("parent Close left physical pane alive", string(out))
	}
}
