package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/coder/websocket"
)

func TestNativeRemoteIOSharedSDKPermissionActualBashResultAndPermanentClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	destination := filepath.Join(f.cwd, "remote-bash-proof.txt")
	command := "printf REAL_REMOTE_BASH_OK > '" + destination + "'"
	var requests atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if requests.Add(1) == 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			input := string(rawMain(map[string]any{"command": command, "description": "actual remote permission Bash"}))
			events := []any{
				map[string]any{"type": "message_start", "message": map[string]any{"id": "remote-tool", "type": "message", "role": "assistant", "model": "fixture-model", "content": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 0}}},
				map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": "remote-bash-tool", "name": "Bash", "input": map[string]any{}}},
				map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": input}},
				map[string]any{"type": "content_block_stop", "index": 0},
				map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "tool_use"}, "usage": map[string]any{"output_tokens": 1}},
				map[string]any{"type": "message_stop"},
			}
			for _, event := range events {
				var row map[string]json.RawMessage
				_ = json.Unmarshal(rawMain(event), &row)
				var name string
				_ = json.Unmarshal(row["type"], &name)
				_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, rawMain(event))
			}
			return
		}
		emitPlanFixture(w, 2, "", "REMOTE_HARNESS_COMPLETED")
	}))
	defer model.Close()
	var approvals atomic.Int32
	remoteDone := make(chan struct{})
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		send := func(value any) {
			if err := conn.Write(ctx, websocket.MessageText, append(rawMain(value), '\n')); err != nil {
				t.Error(err)
			}
		}
		send(map[string]any{"type": "control_request", "request_id": "remote-initialize", "request": map[string]string{"subtype": "initialize"}})
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				if ctx.Err() == nil {
					t.Error(err)
				}
				return
			}
			frame := objMain(data)
			if frame.String("type") == "control_response" && objMain(frame.Get("response")).String("request_id") == "remote-initialize" {
				send(map[string]any{"type": "user", "uuid": "actual-remote-command", "message": map[string]string{"role": "user", "content": "Run real Bash only after approval"}})
			}
			if frame.String("type") == "control_request" && objMain(frame.Get("request")).String("subtype") == "can_use_tool" {
				if objMain(frame.Get("request")).String("tool_name") != "Bash" {
					t.Error(string(data))
					return
				}
				approvals.Add(1)
				send(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": frame.String("request_id"), "response": map[string]any{"behavior": "allow", "updatedInput": map[string]string{"command": command, "description": "actual remote permission Bash"}}}})
			}
			if frame.String("type") == "result" {
				if frame.String("subtype") != "success" || frame.String("result") != "REMOTE_HARNESS_COMPLETED" {
					t.Error(string(data))
				}
				_ = conn.Close(websocket.StatusCode(4001), "original session permanently replaced")
				close(remoteDone)
				return
			}
		}
	}))
	defer remote.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = model.URL
	f.n.Environment["ANTHROPIC_API_KEY"] = "fake-remote-model-key"
	f.n.Environment["CLAUDE_CODE_SESSION_ACCESS_TOKEN"] = "fake-remote-ingress-key"
	f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
	f.n.Environment["PATH"] = "/usr/bin:/bin"
	disabled := false
	var output, diagnostics bytes.Buffer
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{SDKURL: "ws" + strings.TrimPrefix(remote.URL, "http") + "/ws/actual", ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual RemoteIO shared Harness", Tools: []string{"Bash"}, MaxTurns: 4, PermissionMode: "default", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: runtime.GOOS, Input: strings.NewReader(""), Output: &output, Error: &diagnostics})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	code, err := m.Run(ctx)
	if err != nil || code != 0 {
		t.Fatal("actual public Harness RemoteIO failed", code, err, diagnostics.String())
	}
	select {
	case <-remoteDone:
	case <-ctx.Done():
		t.Fatal("remote real result did not arrive")
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "REAL_REMOTE_BASH_OK" || requests.Load() != 2 || approvals.Load() != 1 || output.Len() != 0 {
		t.Fatal("real remote tool side effect/permission/model routing", err, string(data), requests.Load(), approvals.Load(), output.String())
	}
}
