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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func ultracodeMain(t *testing.T, endpoint, effort string, interactive bool, resolve func(context.Context, *cooperative.Scope, queryloop.RegisteredTool, *protocol.JSONObject, queryloop.JSONToolCallContext) (queryloop.ToolPermission, error), configure ...func(*nativePluginFixture)) *MainPrint {
	t.Helper()
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false},"future":{"keep":true}}`)
	f.n.Environment["ANTHROPIC_API_KEY"] = "private-ultracode-fake-key"
	f.n.Environment["ANTHROPIC_BASE_URL"] = endpoint
	f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
	f.n.Environment["ANTHROPIC_DEFAULT_SONNET_MODEL"] = "fixture-model"
	f.n.Environment["ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES"] = "effort,xhigh_effort"
	f.n.Environment["CLAUDE_CODE_TMPDIR"] = f.n.Temp
	f.n.Environment["PATH"] = "/usr/bin:/bin"
	for _, setup := range configure {
		setup(&f)
	}
	disabled := false
	if interactive {
		seedNativeReleaseWarmFixture(t, f, "fixture")
	}
	m, err := NewMainPrint(MainPrintOptions{Interactive: interactive, ResolvePermission: resolve, Arguments: PrintArguments{EffortLevel: effort, ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "private shared Ultracode parent", Tools: []string{"Workflow"}, MaxTurns: 8, PermissionMode: "default", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: runtime.GOOS, Input: bytes.NewReader(nil), Output: io.Discard, Error: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	if err = m.StartSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	return m
}

// This uses the original WorkflowTool permission producer and a production
// NativeMainPrint catalog. No test callback substitutes the Ultracode state.
func TestActualSourceUltracodeSharedNativePermissionState(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	for _, row := range []struct {
		effort             string
		interactive, ultra bool
		behavior           string
	}{{"", true, false, "ask"}, {"ultracode", true, true, "allow"}, {"", false, false, "allow"}, {"ultracode", false, true, "allow"}} {
		t.Run(row.effort+row.behavior, func(t *testing.T) {
			m := ultracodeMain(t, "http://127.0.0.1:1", row.effort, row.interactive, nil)
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-workflow.fixture.ts")
			input, _ := json.Marshal(map[string]any{"homeDir": m.options.Native.Home, "workflowTools": map[string]any{"inputs": []any{}, "results": []any{}, "permissions": []any{map[string]any{"input": map[string]string{"script": "return 1"}, "context": map[string]string{"mode": "default"}, "nonInteractive": !row.interactive, "ultracode": row.ultra}}}})
			cmd := exec.Command(bun, "--no-env-file", fixture)
			cmd.Dir = m.options.Native.Home
			cmd.Env = []string{"HOME=" + m.options.Native.Home, "USERPROFILE=" + m.options.Native.Home, "CLAUDE_CONFIG_DIR=" + m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "TMPDIR=" + m.options.Native.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(m.options.Native.Home), "CC_HAHA_NATIVE_WORKFLOW_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
			cmd.Stdin = bytes.NewReader(input)
			var out, diagnostics bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &diagnostics
			if err := cmd.Run(); err != nil {
				t.Fatal(err, out.String(), diagnostics.String())
			}
			var source struct {
				WorkflowTools struct{ Permissions []protocol.JSONObject }
				Capabilities  struct{ Blocked []string }
			}
			if err := json.Unmarshal(out.Bytes(), &source); err != nil || len(source.Capabilities.Blocked) > 0 || len(source.WorkflowTools.Permissions) != 1 {
				t.Fatal(err, out.String(), diagnostics.String())
			}
			if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				ultra, err := m.nativeWorkflowBindings().Ultracode(scope)
				if err != nil {
					return err
				}
				if ultra != row.ultra {
					t.Errorf("actual AppState Ultracode: got %v want %v", ultra, row.ultra)
				}
				in := protocol.NewJSONObject(protocol.JSONField{Name: "script", Value: quoteMain("return 1")})
				decision, err := m.nativeTools.PermissionDecision(scope, "Workflow", &in)
				if err != nil {
					return err
				}
				if decision.Behavior != source.WorkflowTools.Permissions[0].String("behavior") {
					t.Errorf("actual Source Workflow permission: Go %s Source %s", decision.Behavior, rawMain(source.WorkflowTools.Permissions[0]))
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestActualUltracodeInteractiveWorkflowSharedHarnessLaunchAndReply(t *testing.T) {
	for _, effort := range []string{"", "ultracode"} {
		t.Run(effort, func(t *testing.T) {
			var calls, children, approvals atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if bytes.Contains(body, []byte(`"is_error":true`)) {
					t.Errorf("actual shared tool failed %.3000s", body)
				}
				var request struct{ Messages []protocol.JSONObject }
				_ = json.Unmarshal(body, &request)
				childPrompt := false
				for _, message := range request.Messages {
					if message.String("role") != "user" {
						continue
					}
					var text string
					if json.Unmarshal(message.Get("content"), &text) == nil {
						childPrompt = childPrompt || strings.Contains(text, "Return PHYSICAL_ULTRA_WORKFLOW after inspecting the private task")
					} else {
						var blocks []protocol.JSONObject
						_ = json.Unmarshal(message.Get("content"), &blocks)
						for _, block := range blocks {
							if block.String("type") == "text" && strings.Contains(block.String("text"), "Return PHYSICAL_ULTRA_WORKFLOW after inspecting the private task") {
								childPrompt = true
							}
						}
					}
				}
				if childPrompt {
					children.Add(1)
					emitPlanFixture(w, 1, "", "PHYSICAL_ULTRA_WORKFLOW")
					return
				}
				if calls.Add(1) == 1 {
					if effort == "ultracode" && (!bytes.Contains(body, []byte(`"effort":"xhigh"`)) || !bytes.Contains(body, []byte("Ultracode is on:"))) {
						t.Errorf("actual model lost xhigh/session reminder %.1500s", body)
					}
					emitNativeTaskTool(w, "ultra-workflow", "Workflow", map[string]string{"script": "export const meta={name:'ultra-physical',description:'actual interactive shared Workflow'};return await agent('Return PHYSICAL_ULTRA_WORKFLOW after inspecting the private task')"})
					return
				}
				if calls.Load() == 2 && !bytes.Contains(body, []byte("async_launched")) {
					t.Errorf("actual Workflow launch not consumed %.1500s", body)
				}
				if calls.Load() == 3 && (!bytes.Contains(body, []byte("PHYSICAL_ULTRA_WORKFLOW")) || !bytes.Contains(body, []byte("task-notification"))) {
					t.Errorf("actual completion not returned to parent %.2500s", body)
				}
				emitPlanFixture(w, int(calls.Load()), "", "PHYSICAL_PARENT_REPLY")
			}))
			defer peer.Close()
			m := ultracodeMain(t, peer.URL, effort, true, func(_ context.Context, _ *cooperative.Scope, tool queryloop.RegisteredTool, in *protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolPermission, error) {
				if tool.Name != "Workflow" || call.PermDecision == nil || call.PermDecision.Behavior != "ask" {
					t.Errorf("unexpected actual permission request %s %+v", tool.Name, call.PermDecision)
				}
				approvals.Add(1)
				return queryloop.ToolPermission{Behavior: "allow", Input: in, UpdatedInput: in}, nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": "Explicitly run the private workflow"})})
			if err := m.RunTurn(ctx, input); err != nil {
				t.Fatal(err)
			}
			// The real REPL returns the foreground reply while Workflow remains
			// in AppState. Its idle queue consumer then handles the actual child
			// notification without waiting for every running background task.
			for calls.Load() < 3 {
				if _, err := m.RunLeaderInboxCycle(ctx); err != nil {
					t.Fatal(err)
				}
				if calls.Load() >= 3 {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("actual idle Workflow completion never woke the parent", ctx.Err())
				case <-time.After(10 * time.Millisecond):
				}
			}
			want := int32(1)
			if effort == "ultracode" {
				want = 0
			}
			if approvals.Load() != want || calls.Load() != 3 || children.Load() != 1 {
				t.Errorf("actual shared Harness permission: approvals %d want %d parent requests %d child requests %d", approvals.Load(), want, calls.Load(), children.Load())
			}
			if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				tasks, err := m.Controller.BackgroundTasksOnOwner(scope)
				if err != nil {
					return err
				}
				// Source task/framework evicts notified terminal tasks with no
				// own retain field. Real attachments invoke that poll here.
				if len(tasks) != 0 {
					return fmt.Errorf("Source notified Workflow was not evicted %s", rawMain(tasks))
				}
				history, e := m.Host.OriginalHistoryOnOwner(scope)
				if e != nil {
					return e
				}
				var launch protocol.JSONObject
				completed := false
				completionText := ""
				for _, record := range history {
					if value, ok := record.Record.Object("toolUseResult"); ok && value.String("status") == "async_launched" {
						launch = value
					}
					api, _ := record.Record.Object("message")
					if bytes.Contains(api.Get("content"), []byte("task-notification")) {
						completionText = api.String("content")
					}
					if bytes.Contains(api.Get("content"), []byte("PHYSICAL_ULTRA_WORKFLOW")) && bytes.Contains(api.Get("content"), []byte("task-notification")) {
						completed = true
					}
				}
				if !completed || launch.String("runId") == "" || launch.String("taskId") == "" {
					return fmt.Errorf("actual identity/completion history missing launch=%s notification=%s", rawMain(launch), completionText)
				}
				journal, e := os.ReadFile(filepath.Join(launch.String("transcriptDir"), "journal.jsonl"))
				if e != nil || !bytes.Contains(journal, []byte(`"type":"result"`)) || !bytes.Contains(journal, []byte("PHYSICAL_ULTRA_WORKFLOW")) {
					return fmt.Errorf("actual shared child journal missing %v %s", e, journal)
				}
				directory, e := m.nativeTaskOutputDirectory(scope)
				if e != nil {
					return e
				}
				data, e := os.ReadFile(filepath.Join(directory, launch.String("taskId")+".output"))
				if e != nil {
					return e
				}
				var output protocol.JSONObject
				_ = output.UnmarshalJSON(data)
				if output.String("result") != "PHYSICAL_ULTRA_WORKFLOW" || string(output.Get("agentCount")) != "1" {
					return fmt.Errorf("actual terminal output missing %s", data)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
