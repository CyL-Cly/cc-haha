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
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualDefaultMainPrintSDKPrePostStopCallbacksConsumePhysicalReadAndContinuation(t *testing.T) {
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	after := filepath.Join(f.cwd, "sdk-hook-actual.txt")
	f.write(t, after, "SDK_CALLBACK_PHYSICAL_READ")
	f.n.Environment["ANTHROPIC_API_KEY"] = "fake-sdk-callback"
	f.n.Environment["CLAUDE_CODE_ENTRYPOINT"] = "sdk-cli"
	var mu sync.Mutex
	requests := []protocol.JSONObject{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var request protocol.JSONObject
		if e := request.UnmarshalJSON(data); e != nil {
			t.Error(e)
			return
		}
		mu.Lock()
		requests = append(requests, request.Clone())
		n := len(requests)
		mu.Unlock()
		if n == 1 {
			emitNativeTaskTool(w, "sdk-hook-read", "Read", map[string]any{"file_path": filepath.Join(f.cwd, "never-read-missing.txt")})
		} else {
			if !strings.Contains(string(request.Get("messages")), "SDK_CALLBACK_PHYSICAL_READ") {
				t.Error("default MainPrint did not consume actual callback-updated Read")
			}
			if !strings.Contains(string(request.Get("messages")), "SDK_PRE_ADDITIONAL") || !strings.Contains(string(request.Get("messages")), "SDK_POST_ADDITIONAL") {
				t.Error("actual hook contexts missing from next request")
			}
			if n == 3 && !strings.Contains(string(request.Get("messages")), "SDK_STOP_REQUIRE_MORE") {
				t.Error("Stop callback did not feed same model stack")
			}
			emitPlanFixture(w, n, "", "SDK_CALLBACK_FINISHED")
		}
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	callbackCounts := map[string]int{}
	var diagnostics bytes.Buffer
	output := func(data []byte) error {
		var frame protocol.JSONObject
		if e := frame.UnmarshalJSON(data); e != nil {
			return e
		}
		if frame.String("type") == "result" {
			go func() { _ = writer.Close() }()
			return nil
		}
		if frame.String("type") != "control_request" {
			return nil
		}
		request, _ := frame.Object("request")
		if request.String("subtype") != "hook_callback" {
			return nil
		}
		id := request.String("callback_id")
		mu.Lock()
		callbackCounts[id]++
		count := callbackCounts[id]
		mu.Unlock()
		input, _ := request.Object("input")
		result := map[string]any{}
		switch id {
		case "pre":
			if request.String("tool_use_id") != "sdk-hook-read" || input.String("tool_name") != "Read" {
				t.Error("callback packet lost triggering tool identity", string(rawMain(request)))
			}
			result = map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "PreToolUse", "permissionDecision": "allow", "updatedInput": map[string]any{"file_path": after}, "additionalContext": "SDK_PRE_ADDITIONAL"}}
		case "post":
			if request.String("tool_use_id") != "sdk-hook-read" {
				t.Error("Post callback tool ID", request.String("tool_use_id"))
			}
			actualInput, _ := input.Object("tool_input")
			if actualInput.String("file_path") != after {
				t.Error("Post callback did not observe the actual updated physical input", string(rawMain(actualInput)))
			}
			result = map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "PostToolUse", "additionalContext": "SDK_POST_ADDITIONAL"}}
		case "stop":
			if count == 1 {
				result = map[string]any{"decision": "block", "reason": "SDK_STOP_REQUIRE_MORE"}
			}
		default:
			t.Error("unexpected SDK callback", id)
		}
		packet := append(rawMain(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": frame.String("request_id"), "response": result}}), '\n')
		go func() { _, _ = writer.Write(packet) }()
		return nil
	}
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", PermissionMode: "default", MaxTurns: 100400, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: reader, Output: io.Discard, Error: &diagnostics, OnOutput: output})
	if e != nil {
		t.Fatal(e)
	}
	ctx, finish := context.WithTimeout(context.Background(), 30*time.Second)
	defer finish()
	go func() {
		init := map[string]any{"type": "control_request", "request_id": "sdk-tool-hook-init", "request": map[string]any{"subtype": "initialize", "hooks": map[string]any{"PreToolUse": []any{map[string]any{"matcher": "Read", "hookCallbackIds": []string{"pre", "pre"}}}, "PostToolUse": []any{map[string]any{"matcher": "Read", "hookCallbackIds": []string{"post"}}}, "Stop": []any{map[string]any{"hookCallbackIds": []string{"stop"}}}}}}
		data := append(rawMain(init), '\n')
		data = append(data, rawMain(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "Read the actual private file, then obey the SDK Stop callback"}})...)
		data = append(data, '\n')
		_, _ = writer.Write(data)
	}()
	_, runErr := m.Run(ctx)
	closeErr := m.ShutdownSession(context.Background())
	if runErr != nil || closeErr != nil {
		t.Fatal("actual default SDK callback consumer", runErr, closeErr, diagnostics.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 3 || callbackCounts["pre"] != 2 || callbackCounts["post"] != 1 || callbackCounts["stop"] != 2 {
		t.Fatal("actual physical consumer cardinality", len(requests), callbackCounts, diagnostics.String())
	}
}
