package clibootstrap

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualDefaultMainPrintSDKPermissionWinnerDoesNotWaitForHeldHookAndRetainsLateUpdates(t *testing.T) {
	runActualDefaultSDKPermissionHeldHook(t, "callback")
}
func TestActualDefaultMainPrintSDKPermissionRacePhysicalCommandHTTPPromptAndOriginalAgentError(t *testing.T) {
	for _, kind := range []string{"command", "http", "prompt", "agent"} {
		t.Run(kind, func(t *testing.T) { runActualDefaultSDKPermissionHeldHook(t, kind) })
	}
}
func TestActualDefaultMainPrintSDKPermissionDefinitiveHookAndPassthroughWinners(t *testing.T) {
	for _, kind := range []string{"callback-hook-allow", "callback-hook-deny", "callback-passthrough"} {
		t.Run(kind, func(t *testing.T) { runActualDefaultSDKPermissionHeldHook(t, kind) })
	}
}
func TestActualDefaultMainPrintSDKPermissionLateHookSurvivesCompletedTurnAndNextUser(t *testing.T) {
	runActualDefaultSDKPermissionHeldHook(t, "callback", true)
}
func runActualDefaultSDKPermissionHeldHook(t *testing.T, kind string, completedTurn ...bool) {
	afterResult := len(completedTurn) > 0 && completedTurn[0]
	winner := kind
	if strings.HasPrefix(kind, "callback-") {
		kind = "callback"
	}
	f := worktreeMainFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{}`)
	outside := filepath.Join(f.home, "permission-hook-external.txt")
	f.write(t, outside, "SDK_PERMISSION_RACE_PHYSICAL_READ")
	f.n.Environment["ANTHROPIC_API_KEY"] = "fake-private-permission-hook"
	f.n.Environment["CLAUDE_CODE_ENTRYPOINT"] = "sdk-cli"
	nextRequest := make(chan struct{})
	final := make(chan struct{})
	var finalOnce sync.Once
	releaseFinal := func() { finalOnce.Do(func() { close(final) }) }
	defer releaseFinal()
	var calls atomic.Int32
	var promptCalls atomic.Int32
	hookStarted, hookRelease, hookDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var hookOnce sync.Once
	releaseHook := func() { hookOnce.Do(func() { close(hookRelease) }) }
	defer releaseHook()
	hookOutput := map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "PermissionRequest", "decision": map[string]any{"behavior": "allow", "updatedPermissions": []any{map[string]any{"type": "setMode", "mode": "acceptEdits", "destination": "session"}}}}}
	gate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var input protocol.JSONObject
		_ = input.UnmarshalJSON(body)
		if input.String("hook_event_name") != "PermissionRequest" || input.String("tool_name") != "Read" || input.Get("tool_use_id") != nil {
			t.Error("actual PermissionRequest hook input", string(body))
		}
		close(hookStarted)
		select {
		case <-hookRelease:
			_, _ = w.Write(rawMain(hookOutput))
		case <-r.Context().Done():
		}
		close(hookDone)
	}))
	defer gate.Close()
	if kind != "callback" {
		hook := map[string]any{"type": kind}
		switch kind {
		case "http":
			hook["url"] = gate.URL
		case "prompt", "agent":
			hook["prompt"] = "Evaluate the actual private permission request $ARGUMENTS"
			hook["model"] = "fixture-model"
		case "command":
			script := filepath.Join(f.cwd, "permission-hook-private-peer.py")
			f.write(t, script, "import sys,http.client,json\nvalue=json.load(sys.stdin)\nconn=http.client.HTTPConnection("+string(rawMain(strings.TrimPrefix(gate.URL, "http://")))+")\nconn.request('POST','/',json.dumps(value),{'content-type':'application/json'})\nsys.stdout.write(conn.getresponse().read().decode())\nconn.close()\n")
			hook["command"] = "/usr/bin/python3 " + string(rawMain(script))
		}
		f.write(t, filepath.Join(f.config, "settings.json"), string(rawMain(map[string]any{"hooks": map[string]any{"PermissionRequest": []any{map[string]any{"matcher": "Read", "hooks": []any{hook}}}}})))
	}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "You are evaluating a hook in Claude Code") {
			promptCalls.Add(1)
			close(hookStarted)
			select {
			case <-hookRelease:
				emitPlanFixture(w, 1, "", `{"ok":true}`)
			case <-r.Context().Done():
				t.Error("held prompt model canceled before SDK/normal parent completion")
			}
			close(hookDone)
			return
		}
		n := calls.Add(1)
		if n == 1 {
			emitNativeTaskTool(w, "permission-race-read", "Read", map[string]any{"file_path": outside})
			return
		}
		if winner != "callback-hook-deny" && !strings.Contains(string(body), "SDK_PERMISSION_RACE_PHYSICAL_READ") {
			t.Error("SDK winner did not execute the real Read before held hook completion")
		}
		if winner == "callback-hook-deny" && (!strings.Contains(string(body), "Permission denied by PermissionRequest hook") || strings.Contains(string(body), "SDK_PERMISSION_RACE_PHYSICAL_READ")) {
			t.Error("original hook denial feedback/physical permission", string(body))
		}
		if n > 2 {
			emitPlanFixture(w, int(n), "", "SDK_PERMISSION_NEXT_USER_FINISHED")
			return
		}
		close(nextRequest)
		select {
		case <-final:
			emitPlanFixture(w, 2, "", "SDK_PERMISSION_RACE_FINAL")
		case <-r.Context().Done():
		}
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	frames := make(chan protocol.JSONObject, 64)
	var diagnostic bytes.Buffer
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{PermissionPromptToolName: "stdio", ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", MaxTurns: 100400, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: input, Output: io.Discard, Error: &diagnostic, OnOutput: func(data []byte) error {
		var frame protocol.JSONObject
		if e := frame.UnmarshalJSON(bytes.TrimSpace(data)); e != nil {
			return e
		}
		frames <- frame.Clone()
		return nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, e := m.Run(ctx); done <- e }()
	joined := false
	defer func() {
		_ = writer.Close()
		_ = input.Close()
		if !joined {
			<-done
		}
		_ = m.ShutdownSession(context.Background())
	}()
	send := func(value any) {
		t.Helper()
		data := append(rawMain(value), '\n')
		if _, e := writer.Write(data); e != nil {
			t.Fatal(e)
		}
	}
	initialize := map[string]any{"subtype": "initialize"}
	if kind == "callback" {
		initialize["hooks"] = map[string]any{"PermissionRequest": []any{map[string]any{"matcher": "Read", "hookCallbackIds": []string{"held-permission-hook"}}}}
	}
	send(map[string]any{"type": "control_request", "request_id": "permission-race-init", "request": initialize})
	take := func() protocol.JSONObject {
		t.Helper()
		select {
		case frame := <-frames:
			return frame
		case e := <-done:
			joined = true
			t.Fatal("actual SDK permission hook initialization/run", e, diagnostic.String())
		case <-ctx.Done():
			t.Fatal("actual SDK permission hook frame timeout", context.Cause(ctx), diagnostic.String())
		}
		return protocol.JSONObject{}
	}
	for {
		frame := take()
		response, _ := frame.Object("response")
		if frame.String("type") == "control_response" && response.String("request_id") == "permission-race-init" {
			if response.String("subtype") != "success" {
				t.Fatal("actual public initialize rejected PermissionRequest", string(rawMain(frame)))
			}
			break
		}
	}
	send(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "Read the actual external private file"}})
	var hook, sdk protocol.JSONObject
	for kind == "callback" && !hook.IsObject() || !sdk.IsObject() {
		frame := take()
		request, _ := frame.Object("request")
		switch request.String("subtype") {
		case "hook_callback":
			hook = frame
		case "can_use_tool":
			sdk = frame
		}
	}
	if kind == "command" || kind == "http" || kind == "prompt" {
		select {
		case <-hookStarted:
		case <-ctx.Done():
			t.Fatal("actual held hook producer never began", kind, context.Cause(ctx))
		}
	}
	if winner == "callback-hook-allow" || winner == "callback-hook-deny" {
		selected := hookOutput
		if winner == "callback-hook-deny" {
			selected = map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "PermissionRequest", "decision": map[string]any{"behavior": "deny"}}}
		}
		send(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": hook.String("request_id"), "response": selected}})
		for {
			frame := take()
			if frame.String("type") == "control_cancel_request" {
				if frame.String("request_id") != sdk.String("request_id") {
					t.Fatal("Hook canceled its own producer instead of SDK", string(rawMain(frame)))
				}
				break
			}
		}
	} else {
		if winner == "callback-passthrough" {
			send(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": hook.String("request_id"), "response": map[string]any{}}})
		}
		send(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": sdk.String("request_id"), "response": map[string]any{"behavior": "allow", "updatedInput": map[string]any{}}}})
	}
	select {
	case <-nextRequest:
	case e := <-done:
		joined = true
		t.Fatal("SDK first did not continue physical main turn", e, diagnostic.String())
	case <-ctx.Done():
		t.Fatal("SDK first blocked on held callback", context.Cause(ctx))
	}
	if afterResult {
		releaseFinal()
		for {
			frame := take()
			if frame.String("type") == "result" {
				if string(frame.Get("is_error")) == "true" {
					t.Fatal("normal completed turn", string(rawMain(frame)))
				}
				break
			}
		}
	}
	if kind == "callback" && winner == "callback" {
		send(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": hook.String("request_id"), "response": hookOutput}})
	} else if kind != "agent" && kind != "callback" {
		releaseHook()
		select {
		case <-hookDone:
		case <-ctx.Done():
			t.Fatal("actual held hook did not finish", kind, context.Cause(ctx))
		}
	}

	// The actual SDK control input is FIFO; a subsequent control ACK establishes
	// delivery. The Owner observer then waits for the original hook update job.
	send(map[string]any{"type": "control_request", "request_id": "permission-race-barrier", "request": map[string]any{"subtype": "set_max_thinking_tokens", "max_thinking_tokens": 0}})
	for {
		frame := take()
		response, _ := frame.Object("response")
		if response.String("request_id") == "permission-race-barrier" {
			break
		}
	}
	var mode string
	// ACK establishes input delivery, not consumption by the separately queued
	// original Hook continuation. Observe its real state until that continuation
	// applies the late update; no timer or fabricated hook completion gates it.
	expectedMode := "acceptEdits"
	if winner == "callback-hook-deny" || winner == "callback-passthrough" {
		expectedMode = "default"
	}
	for kind != "prompt" && kind != "agent" && mode != expectedMode && ctx.Err() == nil {
		receipt, failure := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
			permissions, failure := m.Host.NativePermissionContext(scope)
			mode = permissions.String("mode")
			return failure
		})
		if failure == nil {
			failure = receipt.Wait()
		}
		if failure != nil {
			t.Fatal("late hook state observer", failure)
		}
	}
	if kind != "prompt" && kind != "agent" && mode != expectedMode {
		t.Fatal("late hook loser update was discarded", mode, context.Cause(ctx))
	}
	releaseFinal()
	if afterResult {
		send(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "Continue with the real late-updated permission state"}})
	}
	for {
		frame := take()
		if frame.String("type") == "result" {
			if string(frame.Get("is_error")) == "true" {
				t.Fatal("actual main result", string(rawMain(frame)))
			}
			break
		}
	}
	_ = writer.Close()
	e = <-done
	joined = true
	if e != nil && !errors.Is(e, io.EOF) {
		t.Fatal(e, diagnostic.String())
	}
	if kind == "prompt" && promptCalls.Load() != 1 || kind != "prompt" && promptCalls.Load() != 0 {
		t.Fatal("original prompt/agent producer cardinality", kind, promptCalls.Load())
	}
	wantCalls := int32(2)
	if afterResult {
		wantCalls = 3
	}
	if calls.Load() != wantCalls {
		t.Fatal("model request cardinality", calls.Load())
	}
}
