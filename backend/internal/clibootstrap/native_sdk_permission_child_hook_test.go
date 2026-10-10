package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualDefaultMainPrintSDKChildPermissionHookRetainsItsLateSignalAndAgentNamespace(t *testing.T) {
	runActualChildLateSDKHook(t, "callback")
}
func TestActualDefaultMainPrintSDKChildPermissionPromptUsesActualChildToolCatalogAndKeepsStreamingModel(t *testing.T) {
	runActualChildLateSDKHook(t, "prompt")
}
func runActualChildLateSDKHook(t *testing.T, kind string) {
	f := worktreeMainFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{}`)
	if kind == "prompt" {
		f.write(t, filepath.Join(f.config, "settings.json"), string(rawMain(map[string]any{"hooks": map[string]any{"PermissionRequest": []any{map[string]any{"matcher": "Read", "hooks": []any{map[string]any{"type": "prompt", "prompt": "Check actual child permission $ARGUMENTS", "model": "fixture-model"}}}}}})))
	}
	path := filepath.Join(f.home, "child-sdk-permission-external.txt")
	f.write(t, path, "CHILD_LATE_HOOK_PHYSICAL_READ")
	f.n.Environment["ANTHROPIC_API_KEY"] = "fake-child-sdk-permission"
	f.n.Environment["CLAUDE_CODE_ENTRYPOINT"] = "sdk-cli"
	var parentCalls, childCalls, prompts atomic.Int32
	requests := make(chan protocol.JSONObject, 8)
	parentResult := make(chan struct{}, 1)
	promptStarted, promptRelease, promptDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	if kind == "prompt" {
		defer func() {
			select {
			case <-promptRelease:
			default:
				close(promptRelease)
			}
		}()
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body protocol.JSONObject
		_ = body.UnmarshalJSON(data)
		if strings.Contains(string(body.Get("system")), "You are evaluating a hook in Claude Code") {
			var tools []protocol.JSONObject
			_ = json.Unmarshal(body.Get("tools"), &tools)
			if len(tools) != 1 || tools[0].String("name") != "Read" {
				t.Error("child prompt borrowed parent catalog instead of actual child tools", string(body.Get("tools")))
			}
			close(promptStarted)
			select {
			case <-promptRelease:
				emitPlanFixture(w, 1, "", `{"ok":true}`)
			case <-r.Context().Done():
				t.Error("child completion aborted actual late prompt model")
			}
			close(promptDone)
			return
		}
		if strings.Contains(string(body.Get("system")), "SDK_CHILD_DENY_INTERRUPT_SOURCE") {
			n := childCalls.Add(1)
			if n == 1 {
				emitNativeTaskTool(w, "child-deny-read", "Read", map[string]any{"file_path": path})
			} else {
				if !strings.Contains(string(body.Get("messages")), "CHILD_LATE_HOOK_PHYSICAL_READ") {
					t.Error("child did not physically Read before late hook")
				}
				emitPlanFixture(w, int(n), "", "CHILD_FINISHED_BEFORE_LATE_HOOK")
			}
			return
		}
		n := parentCalls.Add(1)
		if n == 1 {
			emitNativeTaskTool(w, "parent-child-deny", "Agent", map[string]any{"description": "SDK permission-controlled child", "prompt": "Read the actual external file", "subagent_type": "sdk-child-deny"})
			return
		}
		if !strings.Contains(string(body.Get("messages")), "CHILD_FINISHED_BEFORE_LATE_HOOK") {
			t.Error("parent did not continue after actual child finished")
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
			parentResult <- struct{}{}
			return nil
		}
		request, _ := frame.Object("request")
		if frame.String("type") == "control_request" {
			requests <- frame.Clone()
			if request.String("subtype") == "can_use_tool" {
				prompts.Add(1)
				response := map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": frame.String("request_id"), "response": map[string]any{"behavior": "allow", "updatedInput": map[string]any{}}}}
				go func() { _, _ = writer.Write(append(rawMain(response), '\n')) }()
			}
		}

		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	go func() {
		initialize := map[string]any{"type": "control_request", "request_id": "child-deny-init", "request": map[string]any{"subtype": "initialize", "hooks": map[string]any{"PermissionRequest": []any{map[string]any{"matcher": "Read", "hookCallbackIds": []string{"child-late-hook"}}}}, "agents": map[string]any{"sdk-child-deny": map[string]any{"description": "Read private input", "prompt": "SDK_CHILD_DENY_INTERRUPT_SOURCE", "tools": []string{"Read"}, "model": "inherit"}}}}
		if kind == "prompt" {
			delete(initialize["request"].(map[string]any), "hooks")
		}
		data := append(rawMain(initialize), '\n')
		data = append(data, rawMain(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "Use sdk-child-deny to read the private external file"}})...)
		data = append(data, '\n')
		_, _ = writer.Write(data)
	}()
	done := make(chan error, 1)
	go func() { _, err := m.Run(ctx); done <- err }()
	joined := false
	defer func() {
		_ = writer.Close()
		_ = input.Close()
		if !joined {
			<-done
		}
		_ = m.ShutdownSession(context.Background())
	}()
	var hook, sdk protocol.JSONObject
	for kind == "callback" && !hook.IsObject() || !sdk.IsObject() {
		select {
		case frame := <-requests:
			request, _ := frame.Object("request")
			switch request.String("subtype") {
			case "hook_callback":
				hook = frame
			case "can_use_tool":
				sdk = frame
			}
		case err := <-done:
			joined = true
			t.Fatal("actual child hook producers", err, diagnostics.String())
		case <-ctx.Done():
			t.Fatal("actual child hook timeout")
		}
	}
	sdkPacket, _ := sdk.Object("request")
	hookPacket, _ := hook.Object("request")
	hookInput, _ := hookPacket.Object("input")
	select {
	case <-parentResult:
	case err := <-done:
		joined = true
		t.Fatal("parent/child stopped before late callback", err, diagnostics.String())
	case <-ctx.Done():
		t.Fatal("held child hook blocked parent completion")
	}
	if kind == "callback" && (hookInput.String("agent_id") != sdkPacket.String("agent_id") || hookInput.String("agent_id") == "") {
		t.Error("original child hook namespace", string(rawMain(hookInput)), string(rawMain(sdkPacket)))
	}
	if kind == "prompt" {
		select {
		case <-promptStarted:
		case <-ctx.Done():
			t.Fatal("actual child prompt never started")
		}
		close(promptRelease)
		select {
		case <-promptDone:
		case <-ctx.Done():
			t.Fatal("actual child prompt never finished")
		}
	} else {
		response := map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": hook.String("request_id"), "response": map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "PermissionRequest", "decision": map[string]any{"behavior": "allow", "updatedPermissions": []any{map[string]any{"type": "setMode", "mode": "acceptEdits", "destination": "session"}}}}}}}
		if _, err = writer.Write(append(rawMain(response), '\n')); err != nil {
			t.Fatal(err)
		}
		mode := ""
		for mode != "acceptEdits" && ctx.Err() == nil {
			receipt, e := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
				state, e := m.Host.NativePermissionContext(scope)
				mode = state.String("mode")
				return e
			})
			if e == nil {
				e = receipt.Wait()
			}
			if e != nil {
				t.Fatal(e)
			}
		}
		if mode != "acceptEdits" {
			t.Error("actual child RunOne canceled parent-held late hook", mode)
		}
	}

	_ = writer.Close()
	err = <-done
	joined = true
	if err != nil {
		t.Fatal(err, diagnostics.String())
	}
	if prompts.Load() != 1 || childCalls.Load() != 2 || parentCalls.Load() != 2 {
		t.Fatal("actual child/parent cardinality", prompts.Load(), childCalls.Load(), parentCalls.Load())
	}
}
