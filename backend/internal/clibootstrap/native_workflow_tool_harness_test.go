package clibootstrap

import (
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

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualWorkflowToolRegistrationParentAndIndependentWorkerCompleteSharedHarness(t *testing.T) {
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	f.n.Environment["ANTHROPIC_API_KEY"] = "private-workflow-tool-fake-key"
	f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
	f.n.Environment["DISABLE_TELEMETRY"] = "1"
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	f.n.Environment["PATH"] = "/usr/bin:/bin"
	f.n.Environment["CLAUDE_CODE_TMPDIR"] = f.n.Temp
	path := filepath.Join(f.n.CWD, "tool-workflow.txt")
	script := `export const meta={name:'full-tool-native',description:'Write and verify real workflow artifact'};return await agent('Write then Read the actual artifact before returning WORKFLOW_CHILD_DONE')`
	var parentRequests, childRequests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var request struct{ Messages []protocol.JSONObject }
		_ = json.Unmarshal(body, &request)
		for _, message := range request.Messages {
			for _, block := range taskBlocks(message.Get("content")) {
				if block.String("type") == "tool_result" && string(block.Get("is_error")) == "true" {
					t.Errorf("actual registered tool failed %s", rawMain(block))
				}
			}
		}
		if strings.Contains(string(body), "You are a subagent spawned by a workflow orchestration script") {
			n := childRequests.Add(1)
			switch n {
			case 1:
				emitNativeTaskTool(w, "wf-child-write", "Write", map[string]any{"file_path": path, "content": "actual-tool-workflow"})
			case 2:
				emitNativeTaskTool(w, "wf-child-read", "Read", map[string]any{"file_path": path})
			default:
				if !strings.Contains(string(body), "actual-tool-workflow") {
					t.Error("worker lost real IO history")
				}
				emitPlanFixture(w, int(n), "", "WORKFLOW_CHILD_DONE")
			}
			return
		}
		n := parentRequests.Add(1)
		if n == 1 {
			emitNativeTaskTool(w, "wf-launch", "Workflow", map[string]any{"script": script})
			return
		}
		if n == 2 {
			if !strings.Contains(string(body), "async_launched") {
				t.Error("registered Workflow lost real launch result")
			}
			emitPlanFixture(w, int(n), "", "WAIT_FOR_WORKFLOW")
			return
		}
		if !strings.Contains(string(body), "<task-notification>") || !strings.Contains(string(body), "WORKFLOW_CHILD_DONE") || !strings.Contains(string(body), "journal.jsonl") {
			t.Errorf("actual notification not consumed by parent physical model %.1800s", body)
		}
		emitPlanFixture(w, int(n), "", "PARENT_WORKFLOW_COMPLETE")
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	disabled := false
	m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual parent shared Workflow harness", Tools: []string{"Workflow,Write,Read"}, MaxTurns: 8, PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: runtime.GOOS, Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if e = m.StartSession(ctx); e != nil {
		t.Fatal(e)
	}
	if e = m.RunTurn(ctx, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoteMain("user")}, protocol.JSONField{Name: "content", Value: quoteMain("Explicitly run a Workflow to write and verify the private artifact")}))})); e != nil {
		t.Fatal(e)
	}
	if parentRequests.Load() != 3 || childRequests.Load() != 3 {
		t.Fatalf("registered actual Workflow not fully drained parent%d child%d", parentRequests.Load(), childRequests.Load())
	}
	data, e := os.ReadFile(path)
	if e != nil || string(data) != "actual-tool-workflow" {
		t.Fatal(e, string(data))
	}
	if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		tasks, e := m.Controller.BackgroundTasksOnOwner(scope)
		if e != nil {
			return e
		}
		if len(tasks) != 1 || tasks[0].String("type") != "local_workflow" || tasks[0].String("status") != "completed" {
			return fmt.Errorf("wrong shared task registry %s", rawMain(tasks))
		}
		messages, e := m.Host.OriginalHistoryOnOwner(scope)
		if e != nil {
			return e
		}
		rows := []json.RawMessage{}
		for _, message := range messages {
			row, e := message.MarshalJSON()
			if e != nil {
				return e
			}
			rows = append(rows, row)
		}
		history, _ := json.Marshal(rows)
		if !strings.Contains(string(history), "PARENT_WORKFLOW_COMPLETE") || !strings.Contains(string(history), "task-notification") {
			return fmt.Errorf("actual history lost Workflow parent continuation %.1500s", history)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
