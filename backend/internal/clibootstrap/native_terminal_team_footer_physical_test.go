package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/testdata/gochildcoverage"
)

func callTerminalTeamFooterActualTool(t *testing.T, ctx context.Context, m *MainPrint, name, input string) protocol.JSONObject {
	t.Helper()
	var value protocol.JSONObject
	err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		_, tools, err := m.nativeTools.Catalog(scope)
		if err != nil {
			return err
		}
		for _, tool := range tools {
			if tool.Name != name {
				continue
			}
			parsed, err := tool.ParseInput(nativeInProcessObject([]byte(input)))
			if err != nil {
				return err
			}
			if tool.ValidateInput != nil {
				if err = tool.ValidateInput(ctx, parsed, &builtinwrite.CallContext{Scope: scope}); err != nil {
					return err
				}
			}
			call := queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}}
			var result queryloop.ToolCallResult
			if tool.StartCallJSON != nil {
				future, err := tool.StartCallJSON(ctx, parsed, call)
				if err != nil {
					return err
				}
				result, err = future.Await(scope)
				if err != nil {
					return err
				}
			} else {
				result, err = tool.CallJSON(ctx, parsed, call)
				if err != nil {
					return err
				}
			}
			return value.UnmarshalJSON(result.Data)
		}
		return fmt.Errorf("actual tool %s missing", name)
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestTerminalTeamFooterRealDaemonToolSpawnShutdownApprovalRosterAndUnsubscribe(t *testing.T) {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("owned actual tmux binary required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "claude-haha-go")
	build := exec.CommandContext(ctx, "go", "build", "-tags=native_process_fixture", "-p=4", "-o", binary, "../../cmd/claude-haha")
	capturePublicGoCoverage(t, build)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	private, err := os.MkdirTemp("/tmp", "footer-pane-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(private)
	var mu sync.Mutex
	var requests []protocol.JSONObject
	var shutdownID string
	var heads atomic.Int32
	artifact := ""
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			if r.Header.Get("Authorization") != "" || r.Header.Get("X-Api-Key") != "" || r.Header.Get("User-Agent") != "Bun/1.3.14" {
				t.Error("actual preconnect identity/auth changed", r.Header)
			}
			heads.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(r.Body)
		input := nativeInProcessObject(body)
		mu.Lock()
		requests = append(requests, input.Clone())
		index := len(requests)
		id := shutdownID
		mu.Unlock()
		switch index {
		case 1:
			if !strings.Contains(string(body), "FOOTER_ACTUAL_CHILD_INITIAL_742") {
				t.Error("actual pane child initial inbox absent")
			}
			emitNativeTaskTool(w, "footer-child-write", "Write", map[string]any{"file_path": artifact, "content": "FOOTER_REAL_DAEMON_CHILD_WRITE_742"})
		case 2:
			emitPlanFixture(w, index, "", "footer child waits for real shutdown request")
		default:
			if id == "" || !strings.Contains(string(body), id) {
				t.Error("actual pending shutdown request missing", id)
			}
			emitNativeTaskTool(w, "footer-child-approve", "SendMessage", map[string]any{"to": "team-lead", "message": map[string]any{"type": "shutdown_response", "request_id": id, "approve": true}})
		}
	}))
	defer provider.Close()
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
		if directory := gochildcoverage.IndirectEnvironment(t, binary); directory != "" {
			n.Environment["GOCOVERDIR"] = directory
		}
		n.Environment["CC_HAHA_NATIVE_FIXTURE_ROOT"] = n.Home
		n.Environment["TMPDIR"] = n.Temp
		n.Environment["PATH"] = filepath.Dir(tmux) + ":/usr/bin:/bin"
		n.Environment["TMUX_TMPDIR"] = private
		n.Environment["TERM"] = "xterm-256color"
		n.Environment["ANTHROPIC_BASE_URL"] = provider.URL
		n.Environment["CLAUDE_CODE_TEAMMATE_COMMAND"] = binary
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		n.Environment["CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"] = "1"
		delete(n.Environment, "TMUX")
		delete(n.Environment, "TERM_PROGRAM")
		delete(n.Environment, "ITERM_SESSION_ID")
	}, func(o *MainPrintOptions) {
		o.Interactive = true
		o.Arguments.TeammateMode = "tmux"
		o.Arguments.Tools = []string{"TeamCreate,TeamDelete,Agent,Read,Write,SendMessage"}
		o.Error = io.Discard
	})
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := m.ShutdownSession(closeCtx); err != nil {
			t.Error(err)
		}
	}()
	artifact = filepath.Join(m.workingDirectory(), "footer-child-write.txt")
	_ = os.WriteFile(filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"alwaysThinkingEnabled":false,"permissions":{"defaultMode":"bypassPermissions"}}`), 0600)
	changes := make(chan bool, 8)
	initial, unsubscribe, err := m.SubscribeTerminalTeamFooter(ctx, func(value bool) { changes <- value })
	if err != nil || initial {
		t.Fatal(initial, err)
	}
	defer unsubscribe()
	callTerminalTeamFooterActualTool(t, ctx, m, "TeamCreate", `{"team_name":"footer-real-daemon"}`)
	select {
	case value := <-changes:
		t.Fatal("leader-only roster must not publish pane-team", value)
	default:
	}
	result := callTerminalTeamFooterActualTool(t, ctx, m, "Agent", `{"description":"Actual footer pane worker","team_name":"footer-real-daemon","name":"worker","prompt":"FOOTER_ACTUAL_CHILD_INITIAL_742","subagent_type":"general-purpose"}`)
	pane := result.String("tmux_pane_id")
	if pane == "" {
		t.Fatal("actual Agent did not start a pane", string(rawMain(result)))
	}
	select {
	case value := <-changes:
		if !value {
			t.Fatal("real nonlead roster did not publish true")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for {
		data, err := os.ReadFile(artifact)
		mu.Lock()
		count := len(requests)
		mu.Unlock()
		if err == nil && string(data) == "FOOTER_REAL_DAEMON_CHILD_WRITE_742" && count >= 2 {
			break
		}
		select {
		case <-ctx.Done():
			capture := exec.Command(tmux, "-L", fmt.Sprintf("claude-swarm-%d", m.options.PID), "capture-pane", "-t", pane, "-p", "-S", "-200")
			capture.Env = nativeAgentProcessEnvironment(m.options.Native)
			capturePublicGoCoverage(t, capture)
			output, _ := capture.CombinedOutput()
			t.Fatal("real child model/write failed", ctx.Err(), string(output))
		case <-time.After(10 * time.Millisecond):
		}
	}
	shutdown := callTerminalTeamFooterActualTool(t, ctx, m, "SendMessage", `{"to":"worker","message":{"type":"shutdown_request","reason":"Actual footer fixture requests joined shutdown"}}`)
	mu.Lock()
	shutdownID = shutdown.String("request_id")
	if shutdownID == "" {
		shutdownID = shutdown.String("requestId")
	}
	mu.Unlock()
	if shutdownID == "" {
		t.Fatal("actual shutdown tool receipt omitted ID", string(rawMain(shutdown)))
	}
	for {
		if _, err = m.PollInteractiveLeaderInbox(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case value := <-changes:
			if value {
				t.Fatal("accepted shutdown did not remove actual roster")
			}
			goto removed
		default:
		}
		select {
		case <-ctx.Done():
			t.Fatal("real shutdown approval not consumed", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
removed:
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		value, err := m.terminalPaneTeamEnabled(scope)
		if err != nil {
			return err
		}
		if value {
			return fmt.Errorf("removed roster still enabled")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	unsubscribe()
	callTerminalTeamFooterActualTool(t, ctx, m, "TeamDelete", `{}`)
	select {
	case value := <-changes:
		t.Fatal("unsubscribed listener invoked", value)
	default:
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	list := exec.CommandContext(ctx, tmux, "-L", fmt.Sprintf("claude-swarm-%d", m.options.PID), "list-panes", "-a", "-F", "#{pane_id}")
	list.Env = nativeAgentProcessEnvironment(m.options.Native)
	capturePublicGoCoverage(t, list)
	output, _ := list.CombinedOutput()
	if strings.Contains(string(output), pane) {
		t.Fatal("actual stopped child pane survived owner Close", string(output))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) < 3 {
		t.Fatal("no actual child shutdown model consumer", len(requests))
	}
	if heads.Load() != 1 {
		t.Fatal("actual child preconnect must stay separate from model requests", heads.Load())
	}
	for _, request := range requests {
		if !json.Valid(rawMain(request)) {
			t.Fatal("invalid detached model receipt")
		}
	}
}
