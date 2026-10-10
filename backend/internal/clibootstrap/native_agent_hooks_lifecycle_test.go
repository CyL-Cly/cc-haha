package clibootstrap

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/agenthook"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryhooks"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestActualAgentHookBashUsesTemporaryContextWithoutMovingParentCwd(t *testing.T) {
	var calls atomic.Int32
	var childDirectory string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			emitNativeTaskTool(w, "actual-hook-bash", "Bash", map[string]any{"command": "cd '" + childDirectory + "' && printf 'ACTUAL_HOOK_BASH_CWD_583'"})
			return
		}
		emitNativeTaskTool(w, "actual-hook-bash-result", "StructuredOutput", map[string]any{"ok": true})
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		childDirectory = filepath.Join(n.CWD, "hook-directory")
		if e := os.MkdirAll(childDirectory, 0700); e != nil {
			t.Fatal(e)
		}
	}, func(o *MainPrintOptions) { o.Arguments.AllowedTools = []string{"Bash"} })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	originalCwd := m.workingDirectory()
	if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		path, e := m.Controller.Writer().ComputedTranscriptPath(scope)
		if e != nil {
			return e
		}
		prompt := messages.OrderedUser(m.factory.CreateUser(storage.Object{"content": quoteMain("Inspect Bash in actual hook context")}))
		input := queryhooks.AgentModelInput{Hook: protocol.NewJSONObject(), Prompt: prompt, HookAgentID: "hook-agent-bash-583", Model: "fixture-model", SystemPrompt: agenthook.SystemPrompt(path), TranscriptPath: path, ToolUseContext: &builtinwrite.CallContext{Scope: scope, MainLoopModel: "fixture-model", PermissionContext: m.Host.NativePermissionContext}}
		e = m.Host.QueryAgentHook(ctx, scope, input, func(message queryloop.Message) (bool, error) {
			a, _ := message.Record.Object("attachment")
			return a.String("type") != "structured_output", nil
		})
		if e != nil {
			return e
		}
		m.hook.ClearNativeAgentHooks(input.HookAgentID)
		if m.workingDirectory() != originalCwd {
			return fmt.Errorf("Hook Bash changed parent cwd: %s", m.workingDirectory())
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 2 {
		t.Fatal("real hook Bash loop", calls.Load())
	}
	if paths, _ := filepath.Glob(filepath.Join(m.options.Native.Temp, "claude-*-cwd")); len(paths) != 0 {
		t.Fatal("native cwd files leaked", paths)
	}
	if e := m.ShutdownSession(ctx); e != nil {
		t.Fatal(e)
	}
}

func TestActualAgentHookCancellationJoinsPhysicalModelAndNativeReferences(t *testing.T) {
	entered, closed := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"hook-cancel\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture-model\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
		w.(http.Flusher).Flush()
		close(entered)
		<-r.Context().Done()
		close(closed)
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) { delete(n.Environment, "CLAUDE_CODE_SIMPLE") }, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	model, cancelModel := context.WithCancel(ctx)
	defer cancelModel()
	done := make(chan error, 1)
	go func() {
		done <- m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			path, e := m.Controller.Writer().ComputedTranscriptPath(scope)
			if e != nil {
				return e
			}
			prompt := messages.OrderedUser(m.factory.CreateUser(storage.Object{"content": quoteMain("Cancel this actual hook model")}))
			interrupted := false
			e = m.Host.QueryAgentHook(model, scope, queryhooks.AgentModelInput{Hook: protocol.NewJSONObject(), Prompt: prompt, HookAgentID: "hook-agent-cancel-341", Model: "fixture-model", SystemPrompt: agenthook.SystemPrompt(path), TranscriptPath: path, ToolUseContext: &builtinwrite.CallContext{Scope: scope, MainLoopModel: "fixture-model"}}, func(message queryloop.Message) (bool, error) {
				interrupted = interrupted || message.Type() == "user" && strings.Contains(string(rawMain(message.Record)), "Request interrupted by user")
				return true, nil
			})
			// Source query may return normally with its real interruption user;
			// execAgentHook then classifies absent structured output as cancelled.
			if e != nil && !errors.Is(e, context.Canceled) {
				return fmt.Errorf("hook model lost cancellation: %v", e)
			}
			if e == nil && !interrupted {
				return fmt.Errorf("actual interrupted query yielded no interruption")
			}
			return nil
		})
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cancelModel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("actual query did not join after cancel", ctx.Err())
	}
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("actual model transport did not close", ctx.Err())
	}
	if e := m.ShutdownSession(ctx); e != nil {
		t.Fatal(e)
	}
	// ShutdownSession joins the shared Writer's real native-job ledger and the
	// same Owner before returning; no detached helper or replacement ledger.
	if !m.shuttingDown {
		t.Fatal("actual session did not finish shutdown")
	}
}
