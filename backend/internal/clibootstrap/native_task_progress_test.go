package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func emitNativeTaskTool(w http.ResponseWriter, id, name string, input any) {
	w.Header().Set("Content-Type", "text/event-stream")
	tool, _ := json.Marshal(name)
	encoded, _ := json.Marshal(input)
	partial, _ := json.Marshal(string(encoded))
	fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"task-progress-message\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture-model\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
	fmt.Fprintf(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":%q,\"name\":%s,\"input\":{}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":%s}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", id, tool, partial)
}
func TestActualTaskOutputWaitingProgressCompletesCanonicalHarnessResult(t *testing.T) {
	f := newNativePluginFixture(t)
	f.write(t, f.config+"/settings.json", `{"enabledPlugins":{"sample@mkt":false}}`)
	f.n.Environment["ANTHROPIC_API_KEY"] = "private-task-progress-key"
	f.n.Environment["PATH"] = "/usr/bin:/bin"
	f.n.Environment["CLAUDE_CODE_TMPDIR"] = f.n.Temp
	f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	var requests atomic.Int32
	var taskID string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		round := requests.Add(1)
		switch round {
		case 1:
			emitNativeTaskTool(w, "native-bash-start", "Bash", map[string]any{"command": "sleep 0.5; printf actual-progress-complete", "run_in_background": true, "description": "actual waiting task"})
		case 2:
			var request struct{ Messages []protocol.JSONObject }
			_ = json.Unmarshal(body, &request)
			for _, message := range request.Messages {
				var blocks []protocol.JSONObject
				_ = json.Unmarshal(message.Get("content"), &blocks)
				for _, block := range blocks {
					if block.String("type") == "tool_result" {
						text := block.String("content")
						marker := "Command running in background with ID: "
						if i := strings.Index(text, marker); i >= 0 {
							taskID = strings.TrimSuffix(strings.Fields(text[i+len(marker):])[0], ".")
						}
					}
				}
			}
			if taskID == "" {
				t.Error("actual background task handle absent")
			}
			emitNativeTaskTool(w, "native-output-wait", "TaskOutput", map[string]any{"task_id": taskID, "block": true, "timeout": 5000})
		case 3:
			if !strings.Contains(string(body), "actual-progress-complete") || !strings.Contains(string(body), "<retrieval_status>success</retrieval_status>") {
				t.Errorf("actual TaskOutput failed rather than completing: %.2000s", body)
			}
			emitPlanFixture(w, 3, "", "ACTUAL_TASK_PROGRESS_OK")
		default:
			if round > 4 {
				t.Error("duplicate task follow-up")
			}
			emitPlanFixture(w, int(round), "", "ACTUAL_TASK_PROGRESS_OK")
		}
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	disabled := false
	frames := []protocol.JSONObject{}
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "Actual native TaskOutput progress reproduction", Tools: []string{"Bash,TaskOutput,TaskStop"}, MaxTurns: 5, PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: runtime.GOOS, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, OnOutput: func(data []byte) error { frames = append(frames, taskObject(t, data)); return nil }})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	if e = m.StartSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Start and wait for real background output"})})
	if e = m.RunTurn(ctx, input); e != nil {
		t.Fatal(e)
	}
	result := false
	for _, frame := range frames {
		if frame.String("type") == "result" {
			if string(frame.Get("is_error")) == "true" {
				t.Fatal(frame)
			}
			result = true
		}
	}
	if !result {
		t.Fatal("actual task progress produced no canonical result")
	}
	history, _ := json.Marshal(m.Controller.History())
	if !strings.Contains(string(history), "waiting_for_task") {
		t.Fatal("progress producer did not cross actual SDK ASK boundary")
	}
	if strings.Contains(string(history), "nonordinary ASK yield progress") {
		t.Fatal("original live failure persisted")
	}
}
