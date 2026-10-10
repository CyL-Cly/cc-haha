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
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	nativeServer "github.com/NanmiCoder/cc-haha/backend/internal/server"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
)

type taskPublicationCapture struct {
	mu     sync.Mutex
	events []protocol.JSONObject
}

func (c *taskPublicationCapture) enqueue(raw []byte) error {
	var event protocol.JSONObject
	if err := event.UnmarshalJSON(bytes.TrimSpace(raw)); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event.Clone())
	return nil
}
func (c *taskPublicationCapture) snapshot() []protocol.JSONObject {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]protocol.JSONObject, len(c.events))
	for i, row := range c.events {
		out[i] = row.Clone()
	}
	return out
}
func actualTaskPublicationConsumer(t *testing.T, input any) protocol.JSONObject {
	t.Helper()
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	raw, _ := json.Marshal(input)
	var object map[string]any
	_ = json.Unmarshal(raw, &object)
	object["homeDir"] = home
	raw, _ = json.Marshal(object)
	fixture, _ := filepath.Abs("testdata/native_task_publication.fixture.ts")
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CLAUDE_CODE_TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_TASK_PUBLICATION=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source publication/store consumer: %v %.1800s", err, stderr.String())
	}
	var result protocol.JSONObject
	if err = result.UnmarshalJSON(out); err != nil {
		t.Fatal(err, string(out))
	}
	if string(result.Get("blocked")) != "[]" {
		t.Fatal("Source fixture attempted forbidden capability", result.Get("blocked"))
	}
	return result
}
func TestActualRootWorkflowTerminalReachesSourceDesktopSaveConsumer(t *testing.T) {
	var posts atomic.Int32
	var file string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if posts.Add(1) == 1 {
			emitNativeTaskTool(w, "actual-wf-read", "Read", map[string]any{"file_path": file})
			return
		}
		if !strings.Contains(string(body), "ACTUAL_WORKFLOW_FILE_MARKER") {
			t.Error("actual workflow worker did not consume physical Read")
		}
		emitPlanFixture(w, 2, "", "ACTUAL_WORKFLOW_READ_DONE")
	}))
	defer peer.Close()
	capture := &taskPublicationCapture{}
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) { delete(n.Environment, "CLAUDE_CODE_SIMPLE") }, func(o *MainPrintOptions) {
		o.OnOutput = capture.enqueue
		o.Arguments.Tools = []string{"Workflow,TaskOutput,Read,Write,Bash,Agent"}
	})
	file = filepath.Join(m.options.Native.CWD, "workflow-publication-input.txt")
	if err := os.WriteFile(file, []byte("ACTUAL_WORKFLOW_FILE_MARKER\n17 29\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var launch protocol.JSONObject
	notice := ""
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		launch, err = m.launchNativeWorkflow(ctx, scope, protocol.NewJSONObject(protocol.JSONField{Name: "script", Value: quoteMain(`export const meta={name:'native-save-completion',description:'Actual file consumer'};phase('Read');return await agent('Use Read on the actual file',{label:'Reader'})`)}), quoteMain("actual-workflow-launch"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		entry, err := m.Controller.BackgroundTaskOnOwner(scope, launch.String("taskId"))
		if err != nil {
			return err
		}
		wait, err := nativeSkillWork(m, scope, func() (struct{}, error) { return struct{}{}, entry.Join(ctx) })
		if err != nil {
			return err
		}
		if _, err = wait.Await(scope); err != nil {
			return err
		}
		entry, err = m.Controller.BackgroundTaskOnOwner(scope, launch.String("taskId"))
		if err != nil {
			return err
		}
		if entry.Data.String("status") != "completed" || entry.Data.String("result") != "ACTUAL_WORKFLOW_READ_DONE" {
			t.Fatalf("actual workflow failed %s", rawMain(entry.Data))
		}
		for _, row := range m.Controller.QueueEvents() {
			if row.String("mode") == "task-notification" {
				notice = row.String("value")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if notice == "" || posts.Load() != 2 {
		t.Fatal("actual terminal producer/physical Read missing", posts.Load())
	}
	assertTaskPublicationFIFO(t, capture.snapshot(), launch.String("taskId"), true)
	result := actualTaskPublicationConsumer(t, map[string]any{"sessionId": m.Controller.SessionID(), "before": []any{map[string]any{"mode": "task-notification", "value": notice}}, "goEvents": capture.snapshot()})
	source, _ := result.Object("sourceConsumer")
	actual, _ := result.Object("goConsumer")
	if string(source.Get("completed")) != "1" {
		t.Fatal("actual original Source did not complete save consumer", rawMain(source))
	}
	if string(actual.Get("completed")) != "1" {
		t.Fatalf("Go live completedRuns differs from original Source: Go=%s Source=%s", rawMain(actual), rawMain(source))
	}
	if string(result.Get("retained")) != "1" || string(result.Get("disposedRetained")) != "0" {
		t.Fatal("Source bookend changed model command retention")
	}
}

func assertTaskPublicationFIFO(t *testing.T, events []protocol.JSONObject, id string, requireProgress bool) {
	t.Helper()
	starts, progress, terminals := 0, 0, 0
	terminalSeen := false
	for _, event := range events {
		if event.String("type") != "system" || event.String("task_id") != id {
			continue
		}
		switch event.String("subtype") {
		case "task_started":
			if terminalSeen {
				t.Fatal("start followed terminal", id, events)
			}
			starts++
		case "task_progress":
			if starts != 1 || terminalSeen {
				t.Fatal("progress escaped start/terminal FIFO", id, rawMain(events))
			}
			progress++
		case "task_notification":
			if starts != 1 {
				t.Fatal("terminal preceded actual start", id, events)
			}
			terminals++
			terminalSeen = true
		}
	}
	if starts != 1 || terminals != 1 || requireProgress && progress == 0 {
		t.Fatal("actual start/progress/terminal FIFO missing", id, starts, progress, terminals, rawMain(events))
	}
}

func taskPublicationTerminals(events []protocol.JSONObject, taskID string) []protocol.JSONObject {
	out := []protocol.JSONObject{}
	for _, event := range events {
		if event.String("type") == "system" && event.String("subtype") == "task_notification" && (taskID == "" || event.String("task_id") == taskID) {
			out = append(out, event)
		}
	}
	return out
}
func taskPublicationCanonical(events []protocol.JSONObject) []protocol.JSONObject {
	out := []protocol.JSONObject{}
	for _, event := range taskPublicationTerminals(events, "") {
		row := event.Clone()
		row.Delete("uuid")
		row.Delete("session_id")
		out = append(out, row)
	}
	return out
}
func TestActualSharedTaskPublicationSourceConditionsProjectionAndClose(t *testing.T) {
	capture := &taskPublicationCapture{}
	peer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("queue publication must not open model") }))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, nil, func(o *MainPrintOptions) { o.OnOutput = capture.enqueue })
	cases := []struct{ mode, owner, body string }{
		{"task-notification", "", `<task-notification><task-id>w-source</task-id><tool-use-id>tool-use</tool-use-id><workflow-run-id>wf_source-abc</workflow-run-id><status>completed</status><summary>Same &lt;literal&gt; summary</summary><result>actual root result</result></task-notification>`},
		{"task-notification", "", `<task-notification><task-id>usage</task-id><status>killed</status><usage><total_tokens>\u00a0+12tail</total_tokens><tool_uses>-0</tool_uses><duration_ms>42.9</duration_ms></usage></task-notification>`},
		{"task-notification", "", `<task-notification><task-id>bad-usage</task-id><status>failed</status><usage><total_tokens>x</total_tokens><tool_uses>1</tool_uses></usage></task-notification>`},
		{"task-notification", "owned-agent", `<task-notification><task-id>owned</task-id><status>completed</status></task-notification>`},
		{"task-notification", "", `<task-notification><task-id>progress</task-id><summary>progress only</summary></task-notification>`},
		{"task-notification", "", `<task-notification><task-id>unknown</task-id><status>paused</status></task-notification>`},
		{"task-notification", "", `<task-notification><task-id>spaced</task-id><status> completed </status></task-notification>`},
		{"prompt", "", `<task-notification><task-id>ordinary-user-xml</task-id><status>completed</status></task-notification>`},
	}
	sourceCommands := []any{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		for _, tc := range cases {
			tc.body = strings.ReplaceAll(tc.body, `\u00a0`, "\u00a0")
			command := map[string]any{"mode": tc.mode, "value": tc.body}
			if tc.owner != "" {
				command["agentId"] = tc.owner
			}
			sourceCommands = append(sourceCommands, command)
			if tc.mode == "task-notification" {
				if err := m.Controller.EnqueueTaskNotificationOnOwner(scope, tc.owner, tc.body); err != nil {
					return err
				}
			} else {
				future, err := m.Host.AdmitParsedSDK(scope, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "uuid", Value: quoteMain("ordinary-user")}, protocol.JSONField{Name: "message", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "content", Value: quoteMain(tc.body)}))}))
				if err != nil {
					return err
				}
				if _, err = future.Await(scope); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := capture.snapshot()
	result := actualTaskPublicationConsumer(t, map[string]any{"sessionId": m.Controller.SessionID(), "before": sourceCommands, "disposed": []any{map[string]any{"mode": "task-notification", "value": `<task-id>after-close</task-id><status>completed</status>`}}, "goEvents": before})
	var source []protocol.JSONObject
	_ = json.Unmarshal(result.Get("source"), &source)
	got := taskPublicationCanonical(before)
	want := taskPublicationCanonical(source)
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("shared queue Source projection differs Go=%s Source=%s", gotJSON, wantJSON)
	}
	if len(got) != 3 || string(result.Get("retained")) != "8" || string(result.Get("disposedRetained")) != "1" {
		t.Fatal("publication conditions or Source queue retention changed", len(got), result.Get("retained"))
	}
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		// Mid-round attachment/dequeue never republishes the same queue object.
		_, err := m.Controller.TakeTaskNotificationAttachmentsOnOwner(scope, "")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(taskPublicationTerminals(capture.snapshot(), "")) != 3 {
		t.Fatal("queue mutation duplicated published bookends")
	}
	if err := m.Host.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		return m.Controller.EnqueueTaskNotificationOnOwner(scope, "", `<task-id>after-close</task-id><status>completed</status>`)
	}); err != nil {
		t.Fatal(err)
	}
	if len(taskPublicationTerminals(capture.snapshot(), "")) != 3 {
		t.Fatal("disposed host published a late notification")
	}
}

func joinTaskPublicationProducer(t *testing.T, m *MainPrint, ctx context.Context, id string) protocol.JSONObject {
	t.Helper()
	var result protocol.JSONObject
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		entry, err := m.Controller.BackgroundTaskOnOwner(scope, id)
		if err != nil {
			return err
		}
		if entry == nil {
			t.Fatal("missing actual task", id)
		}
		wait, err := nativeSkillWork(m, scope, func() (struct{}, error) { return struct{}{}, entry.Join(ctx) })
		if err != nil {
			return err
		}
		if _, err = wait.Await(scope); err != nil {
			return err
		}
		entry, err = m.Controller.BackgroundTaskOnOwner(scope, id)
		if err == nil {
			result = entry.Data.Clone()
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return result
}
func TestActualRootAgentBashAndSuppressedXMLTaskStopPublishOnce(t *testing.T) {
	for _, kind := range []string{"agent", "bash", "bash-stop"} {
		t.Run(kind, func(t *testing.T) {
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if kind != "agent" {
					t.Error("shell-only producer opened model")
				}
				emitPlanFixture(w, 1, "", "ACTUAL_ROOT_AGENT_RETURN")
			}))
			defer peer.Close()
			capture := &taskPublicationCapture{}
			m := backgroundAgentMainWithOptions(t, peer.URL, nil, func(o *MainPrintOptions) { o.OnOutput = capture.enqueue })
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			id := ""
			if kind == "agent" {
				if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
					request, err := actualBackgroundAgentRequest(scope, m, "Return the actual root result")
					if err != nil {
						return err
					}
					future, err := m.runBackgroundAgent(ctx, scope, request)
					if err != nil {
						return err
					}
					launched, err := future.Await(scope)
					id = launched.AgentID
					return err
				}); err != nil {
					t.Fatal(err)
				}
			} else {
				command := "printf ACTUAL_ROOT_BASH_RETURN"
				if kind == "bash-stop" {
					command = "sleep 30"
				}
				raw, _ := json.Marshal(map[string]any{"command": command, "run_in_background": true, "description": "Actual root shell"})
				started, _ := taskToolCall(t, m, "Bash", string(raw))
				id = started.String("backgroundTaskId")
				if kind == "bash-stop" {
					_, _ = taskToolCall(t, m, "TaskStop", `{"task_id":"`+id+`"}`)
				}
			}
			terminal := joinTaskPublicationProducer(t, m, ctx, id)
			assertTaskPublicationFIFO(t, capture.snapshot(), id, false)
			events := taskPublicationTerminals(capture.snapshot(), id)
			if len(events) != 1 {
				t.Fatalf("%s duplicated or lost bookend: %s", kind, rawMain(events))
			}
			want := "completed"
			if kind == "bash-stop" {
				want = "stopped"
			}
			if events[0].String("status") != want {
				t.Fatal(kind, rawMain(events[0]), rawMain(terminal))
			}
			notices := []any{}
			if err := m.Owner.Run(ctx, func(*cooperative.Scope) error {
				for _, event := range m.Controller.QueueEvents() {
					if event.String("mode") == "task-notification" {
						notices = append(notices, map[string]any{"mode": "task-notification", "value": event.String("value")})
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if kind == "bash-stop" {
				if len(notices) != 0 {
					t.Fatal("TaskStop no-XML path invented notification")
				}
				return
			}
			result := actualTaskPublicationConsumer(t, map[string]any{"sessionId": m.Controller.SessionID(), "before": notices, "goEvents": capture.snapshot()})
			var source []protocol.JSONObject
			_ = json.Unmarshal(result.Get("source"), &source)
			got, _ := json.Marshal(taskPublicationCanonical(events))
			wantJSON, _ := json.Marshal(taskPublicationCanonical(source))
			if !bytes.Equal(got, wantJSON) {
				t.Fatalf("%s Source root projection differs Go=%s Source=%s", kind, got, wantJSON)
			}
		})
	}
}
func TestActualOwnedWorkflowKeepsIndependentBookendWithoutRootPublication(t *testing.T) {
	capture := &taskPublicationCapture{}
	peer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("constant owned workflow opened model") }))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) { delete(n.Environment, "CLAUDE_CODE_SIMPLE") }, func(o *MainPrintOptions) {
		o.OnOutput = capture.enqueue
		o.AgentID = "owned-parent"
		o.Arguments.Tools = []string{"Workflow,Read"}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var launch protocol.JSONObject
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		launch, err = m.launchNativeWorkflow(ctx, scope, protocol.NewJSONObject(protocol.JSONField{Name: "script", Value: quoteMain(`export const meta={name:'owned-constant',description:'Actual owned lifecycle'};return 'OWNED_ACTUAL_RETURN'`)}), quoteMain("owned-tool"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	task := joinTaskPublicationProducer(t, m, ctx, launch.String("taskId"))
	assertTaskPublicationFIFO(t, capture.snapshot(), launch.String("taskId"), false)
	events := taskPublicationTerminals(capture.snapshot(), launch.String("taskId"))
	if task.String("status") != "completed" || len(events) != 1 || events[0].String("owner_agent_id") != "owned-parent" || events[0].Get("workflow_run_id") != nil || events[0].Get("result") != nil {
		t.Fatalf("owned Source bookend changed %s %s", rawMain(task), rawMain(events))
	}
}

func TestActualRootWorkflowEOFAndCancelPublication(t *testing.T) {
	for _, mode := range []string{"EOF", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			capture := &taskPublicationCapture{}
			var file string
			var parents, children atomic.Int32
			var sawTaskOutput atomic.Bool
			entered, closed := make(chan struct{}), make(chan struct{})
			var enterOnce, closeOnce sync.Once
			script := `export const meta={name:'native-EOF-publication',description:'Actual EOF lifecycle'};phase('Read');return await agent('Use Read on the physical file',{label:'Reader'})`
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if strings.Contains(string(body), "workflow orchestration script") {
					enterOnce.Do(func() { close(entered) })
					if mode == "cancel" {
						<-r.Context().Done()
						closeOnce.Do(func() { close(closed) })
						return
					}
					if children.Add(1) == 1 {
						emitNativeTaskTool(w, "EOF-child-read", "Read", map[string]any{"file_path": file})
						return
					}
					if !strings.Contains(string(body), "ACTUAL_EOF_PHYSICAL_READ") {
						t.Error("EOF child lost actual Read")
					}
					emitPlanFixture(w, 2, "", "ACTUAL_EOF_WORKFLOW_DONE")
					return
				}
				switch n := parents.Add(1); n {
				case 1:
					emitNativeTaskTool(w, "EOF-workflow-launch", "Workflow", map[string]any{"script": script})
				case 2:
					var req struct{ Messages []protocol.JSONObject }
					_ = json.Unmarshal(body, &req)
					taskID := ""
					for _, message := range req.Messages {
						for _, block := range taskBlocks(message.Get("content")) {
							if block.String("type") == "tool_result" {
								var launch protocol.JSONObject
								if launch.UnmarshalJSON([]byte(block.String("content"))) == nil && launch.String("status") == "async_launched" {
									taskID = launch.String("taskId")
								}
							}
						}
					}
					if taskID == "" {
						t.Error("EOF parent lost actual Workflow tool mapping")
						emitPlanFixture(w, int(n), "", "MISSING_LAUNCH")
						return
					}
					emitNativeTaskTool(w, "EOF-workflow-output", "TaskOutput", map[string]any{"task_id": taskID, "block": true, "timeout": 30000})
				default:
					if strings.Contains(string(body), "<retrieval_status>success</retrieval_status>") {
						sawTaskOutput.Store(true)
					}
					emitPlanFixture(w, int(n), "", "EOF_PARENT_DONE")
				}
			}))
			defer peer.Close()
			m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) { delete(n.Environment, "CLAUDE_CODE_SIMPLE") }, func(o *MainPrintOptions) {
				o.OnOutput = capture.enqueue
				o.Arguments.Tools = []string{"Workflow,TaskOutput,Read"}
			})
			file = filepath.Join(m.options.Native.CWD, "EOF-workflow-input.txt")
			if err := os.WriteFile(file, []byte("ACTUAL_EOF_PHYSICAL_READ\n"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			taskID := ""
			if mode == "cancel" {
				if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
					launch, err := m.launchNativeWorkflow(ctx, scope, protocol.NewJSONObject(protocol.JSONField{Name: "script", Value: quoteMain(script)}), quoteMain("actual-cancel-workflow"))
					taskID = launch.String("taskId")
					return err
				}); err != nil {
					t.Fatal(err)
				}
				select {
				case <-entered:
				case <-ctx.Done():
					t.Fatal("physical child did not enter", ctx.Err())
				}
				if _, err := m.StopBackgroundTask(ctx, taskID); err != nil {
					t.Fatal("actual SDK stop_task control failed", err)
				}
				task := joinTaskPublicationProducer(t, m, ctx, taskID)
				if task.String("status") != "killed" {
					t.Fatal(rawMain(task))
				}
				select {
				case <-closed:
				case <-ctx.Done():
					t.Fatal("physical child transport did not cancel", ctx.Err())
				}
			} else {
				input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoteMain("user")}, protocol.JSONField{Name: "content", Value: quoteMain("Run the actual Workflow then wait with TaskOutput")}))})
				m.options.Input = strings.NewReader(string(rawMain(input)) + "\n")
				code, err := m.Run(ctx)
				if err != nil || code != 0 {
					t.Fatal("actual stdin EOF failed", code, err)
				}
				if children.Load() != 2 || !sawTaskOutput.Load() {
					t.Fatal("EOF did not execute physical Read and actual TaskOutput", parents.Load(), children.Load(), sawTaskOutput.Load())
				}
			}
			events := taskPublicationTerminals(capture.snapshot(), taskID)
			workflowEvents := []protocol.JSONObject{}
			for _, event := range events {
				if event.String("workflow_run_id") != "" {
					workflowEvents = append(workflowEvents, event)
				}
			}
			if len(workflowEvents) != 1 {
				t.Fatalf("%s workflow bookend lost/duplicated %s", mode, rawMain(workflowEvents))
			}
			want := "completed"
			assertTaskPublicationFIFO(t, capture.snapshot(), events[0].String("task_id"), true)
			if mode == "cancel" {
				want = "stopped"
			}
			if workflowEvents[0].String("status") != want {
				t.Fatal(mode, rawMain(workflowEvents))
			}
			notices := []any{}
			for _, row := range m.Controller.QueueEvents() {
				if row.String("mode") == "task-notification" {
					notices = append(notices, map[string]any{"mode": "task-notification", "value": row.String("value")})
				}
			}
			input := map[string]any{"sessionId": m.Controller.SessionID(), "before": notices, "goEvents": capture.snapshot()}
			if mode == "EOF" {
				input["hydrateURL"] = actualTaskPublicationColdServer(t, m)
			}
			result := actualTaskPublicationConsumer(t, input)
			var source []protocol.JSONObject
			_ = json.Unmarshal(result.Get("source"), &source)
			got, _ := json.Marshal(taskPublicationCanonical(workflowEvents))
			wantJSON, _ := json.Marshal(taskPublicationCanonical(source))
			if !bytes.Equal(got, wantJSON) {
				t.Fatalf("%s Source publication differs Go=%s Source=%s", mode, got, wantJSON)
			}
			if mode == "EOF" {
				for _, name := range []string{"coldConsumer", "liveAfterHydrate"} {
					consumer, _ := result.Object(name)
					if string(consumer.Get("completed")) != "1" {
						t.Fatalf("actual EOF transcript HTTP/store %s lost completed run: %s", name, rawMain(consumer))
					}
				}
			}
			actual, _ := result.Object("goConsumer")
			sourceConsumer, _ := result.Object("sourceConsumer")
			if string(actual.Get("completed")) != string(sourceConsumer.Get("completed")) {
				t.Fatal("EOF/cancel Source desktop filter changed", rawMain(actual), rawMain(sourceConsumer))
			}
		})
	}
}

func TestActualRootAgentCompletionAfterPublisherCloseDoesNotFallback(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		emitPlanFixture(w, 1, "", "ACTUAL_LATE_ROOT_AGENT_DONE")
	}))
	defer peer.Close()
	defer unblock.Do(func() { close(release) })
	capture := &taskPublicationCapture{}
	m := backgroundAgentMainWithOptions(t, peer.URL, nil, func(o *MainPrintOptions) { o.OnOutput = capture.enqueue })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id := ""
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		request, err := actualBackgroundAgentRequest(scope, m, "Return after the actual subscriber closes")
		if err != nil {
			return err
		}
		future, err := m.runBackgroundAgent(ctx, scope, request)
		if err != nil {
			return err
		}
		launched, err := future.Await(scope)
		id = launched.AgentID
		return err
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := m.Host.Close(ctx); err != nil {
		t.Fatal(err)
	}
	unblock.Do(func() { close(release) })
	task := joinTaskPublicationProducer(t, m, ctx, id)
	if task.String("status") != "completed" {
		t.Fatal("late physical child failed", rawMain(task))
	}
	if got := taskPublicationTerminals(capture.snapshot(), id); len(got) != 0 {
		t.Fatal("closed Root publisher fell back to direct bookend", rawMain(got))
	}
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		queued, err := m.Controller.HasQueuedTaskNotificationsOnOwner(scope)
		if err == nil && !queued {
			t.Fatal("subscriber disposal consumed original model command")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// Reopen the actual EOF-created canonical transcript through the real server
// and workflow history API. The desktop's unchanged API/store does hydration.
func actualTaskPublicationColdServer(t *testing.T, m *MainPrint) string {
	t.Helper()
	n := m.options.Native
	settings, err := NewNativeProcessSettings(n, runtime.GOOS, DefaultState())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = settings.Close(context.Background()) })
	owner := cooperative.New()
	t.Cleanup(owner.Close)
	budget := sessionstore.NewBudget()
	var cold *nativeServer.Server
	history, err := workflow.NewHistory(workflow.HistoryOptions{ConfigDir: n.Environment["CLAUDE_CONFIG_DIR"], TempDir: filepath.Join(n.Temp, "claude-"+strconv.Itoa(os.Getuid())), Files: workflow.NativeHistoryFiles(), Reader: sessionstore.NewReaderWithBudget(nil, budget), Notifications: func(ctx context.Context, sid string) ([]protocol.JSONObject, error) {
		return cold.WorkflowTaskNotifications(ctx, sid)
	}})
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := settings.WorkflowDefinitions(owner, n.CWD, history)
	if err != nil {
		t.Fatal(err)
	}
	cold, err = nativeServer.New(nativeServer.Options{HomeDir: n.Home, ConfigDir: n.Environment["CLAUDE_CONFIG_DIR"], WorkDir: n.CWD, Version: "private-publication-hydrate", HistoryBudget: budget, Workflows: definitions, Factory: func(context.Context, nativeServer.SessionOptions, nativeServer.Emit) (nativeServer.LiveSession, error) {
		t.Error("cold hydrate attempted to launch model")
		return nil, fmt.Errorf("model forbidden")
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cold.Close(context.Background()) })
	httpPeer := httptest.NewServer(cold)
	t.Cleanup(httpPeer.Close)
	return httpPeer.URL
}

func TestActualRootNotificationPublisherKeepsOriginalInterruptedPromptResume(t *testing.T) {
	const sid = "a437c5e6-6549-42be-8071-686d22869494"
	peer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("resume should not open model") }))
	defer peer.Close()
	result := actualTaskPublicationConsumer(t, map[string]any{"sessionId": sid, "before": []any{map[string]any{"mode": "prompt", "value": "Original interrupted prompt"}}, "goEvents": []any{}})
	if string(result.Get("source")) != "[]" || string(result.Get("retained")) != "1" {
		t.Fatal("original Source prompt observer changed", result)
	}
	capture := &taskPublicationCapture{}
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		n.Environment["CLAUDE_CODE_RESUME_INTERRUPTED_TURN"] = "1"
		row := map[string]any{"type": "user", "uuid": "10000000-0000-4000-8000-000000000001", "timestamp": "2023-11-14T22:13:20.000Z", "cwd": n.CWD, "sessionId": sid, "parentUuid": nil, "message": map[string]any{"role": "user", "content": "Original interrupted prompt"}}
		path := filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "projects", projectpath.Sanitize(n.CWD), sid+".jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(rawMain(row), '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) { o.Arguments.Resume = sid; o.OnOutput = capture.enqueue })
	if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		events := m.Controller.QueueEvents()
		if len(events) != 1 || events[0].String("mode") != "prompt" || events[0].String("value") != "Original interrupted prompt" {
			t.Fatal("interrupted prompt not retained", events)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(taskPublicationTerminals(capture.snapshot(), "")) != 0 {
		t.Fatal("prompt resume invented SDK terminal")
	}
}

func TestActualWorkflowRegistrationHasOriginalSourceStartProjection(t *testing.T) {
	for _, owner := range []string{"", "owned-parent"} {
		t.Run(owner, func(t *testing.T) {
			script := `export const meta={name:'actual-start',description:'Actual shared start projection'};phase('Actual phase');return 'ACTUAL_START_RETURN'`
			peer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("constant workflow opened model") }))
			defer peer.Close()
			capture := &taskPublicationCapture{}
			m := backgroundAgentMainWithOptions(t, peer.URL, nil, func(o *MainPrintOptions) { o.AgentID = owner; o.OnOutput = capture.enqueue })
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			var launch protocol.JSONObject
			if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				var err error
				launch, err = m.launchNativeWorkflow(ctx, scope, protocol.NewJSONObject(protocol.JSONField{Name: "script", Value: quoteMain(script)}), quoteMain("actual-source-start-tool"))
				return err
			}); err != nil {
				t.Fatal(err)
			}
			_ = joinTaskPublicationProducer(t, m, ctx, launch.String("taskId"))
			result := actualTaskPublicationConsumer(t, map[string]any{"sessionId": m.Controller.SessionID(), "sourceWorkflow": map[string]any{"script": script, "taskId": launch.String("taskId"), "runId": launch.String("runId"), "toolUseId": "actual-source-start-tool", "ownerAgentId": owner}})
			var source []protocol.JSONObject
			_ = json.Unmarshal(result.Get("source"), &source)
			starts := func(events []protocol.JSONObject) []protocol.JSONObject {
				rows := []protocol.JSONObject{}
				for _, event := range events {
					if event.String("type") == "system" && event.String("subtype") == "task_started" && event.String("task_id") == launch.String("taskId") {
						row := event.Clone()
						row.Delete("uuid")
						row.Delete("session_id")
						rows = append(rows, row)
					}
				}
				return rows
			}
			got, want := rawMain(starts(capture.snapshot())), rawMain(starts(source))
			if string(want) == "[]" {
				t.Fatal("actual Source launch did not produce start", rawMain(source))
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("actual Workflow Source start projection differs Go=%s Source=%s", got, want)
			}
			assertTaskPublicationFIFO(t, capture.snapshot(), launch.String("taskId"), true)
		})
	}
}
