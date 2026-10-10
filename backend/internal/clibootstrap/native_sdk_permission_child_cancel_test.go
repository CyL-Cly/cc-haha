package clibootstrap

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualDefaultMainPrintSDKChildDenyInterruptCancelsOnlyActualChildRunOne(t *testing.T) {
	f := worktreeMainFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{}`)
	path := filepath.Join(f.home, "child-sdk-permission-external.txt")
	f.write(t, path, "CHILD_READ_MUST_NEVER_COMPLETE")
	f.n.Environment["ANTHROPIC_API_KEY"] = "fake-child-sdk-permission"
	f.n.Environment["CLAUDE_CODE_ENTRYPOINT"] = "sdk-cli"
	var parentCalls, childCalls, prompts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body protocol.JSONObject
		_ = body.UnmarshalJSON(data)
		if strings.Contains(string(body.Get("system")), "SDK_CHILD_DENY_INTERRUPT_SOURCE") {
			if childCalls.Add(1) != 1 {
				t.Error("SDK interrupt did not stop actual child RunOne before another model request")
			}
			emitNativeTaskTool(w, "child-deny-read", "Read", map[string]any{"file_path": path})
			return
		}
		n := parentCalls.Add(1)
		if n == 1 {
			emitNativeTaskTool(w, "parent-child-deny", "Agent", map[string]any{"description": "SDK permission-controlled child", "prompt": "Read the actual external file", "subagent_type": "sdk-child-deny"})
			return
		}
		if strings.Contains(string(body.Get("messages")), "CHILD_READ_MUST_NEVER_COMPLETE") {
			t.Error("child denial executed real Read")
		}
		emitPlanFixture(w, int(n), "", "PARENT_CONTINUED_AFTER_CHILD_DENY_INTERRUPT")
	}))
	defer server.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = server.URL
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	var diagnostics bytes.Buffer
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{PermissionPromptToolName: "stdio", ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", MaxTurns: 100400, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: input, Output: io.Discard, Error: &diagnostics, OnOutput: func(data []byte) error {
		var frame protocol.JSONObject
		if err := frame.UnmarshalJSON(bytes.TrimSpace(data)); err != nil {
			return err
		}
		if frame.String("type") == "result" {
			go func() { _ = writer.Close() }()
			return nil
		}
		request, _ := frame.Object("request")
		if frame.String("type") == "control_request" && request.String("subtype") == "can_use_tool" {
			prompts.Add(1)
			if request.String("tool_name") != "Read" || request.String("agent_id") == "" {
				t.Error("actual permission request lost child namespace", string(rawMain(request)))
			}
			response := map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": frame.String("request_id"), "response": map[string]any{"behavior": "deny", "message": "Source SDK denied child and interrupted its controller", "interrupt": true}}}
			go func() { _, _ = writer.Write(append(rawMain(response), '\n')) }()
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	go func() {
		initialize := map[string]any{"type": "control_request", "request_id": "child-deny-init", "request": map[string]any{"subtype": "initialize", "agents": map[string]any{"sdk-child-deny": map[string]any{"description": "Read private input", "prompt": "SDK_CHILD_DENY_INTERRUPT_SOURCE", "tools": []string{"Read"}, "model": "inherit"}}}}
		data := append(rawMain(initialize), '\n')
		data = append(data, rawMain(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "Use sdk-child-deny to read the private external file"}})...)
		data = append(data, '\n')
		_, _ = writer.Write(data)
	}()
	_, runErr := m.Run(ctx)
	closeErr := m.ShutdownSession(context.Background())
	if runErr != nil || closeErr != nil {
		t.Fatal("SDK child interrupt affected parent or stranded owner", runErr, closeErr, diagnostics.String())
	}
	if prompts.Load() != 1 || childCalls.Load() != 1 || parentCalls.Load() != 2 {
		t.Fatal("actual child vs parent cancellation counts", prompts.Load(), childCalls.Load(), parentCalls.Load(), diagnostics.String())
	}
}
