package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
)

func TestActualWorkflowAndInProcessTaskUpdateViewMatchesWholeSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned original Workflow and in-process caller required")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-task-list-workflow-teammate.fixture.ts")
	agentDefinition := map[string]any{"description": "Original TaskUpdate state callback", "prompt": "Original Workflow TaskUpdate parent state", "tools": []string{"TaskUpdate"}}
	agentsJSON := protocol.NewJSONObject(fieldMain("task-context-fixture", agentDefinition))
	command := exec.Command(bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TASK_WORKFLOW_SOURCE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_FAST_MODE=1", "PATH=/usr/bin:/bin", "COLUMNS=100", "LINES=36", "FORCE_COLOR=3"}
	command.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "agents": agentsJSON}))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("Actual original Workflow/in-process task callback", err, string(output))
	}
	var source struct {
		Workflow struct {
			ExpandedView string
			Global       protocol.JSONObject
			Requests     []protocol.JSONObject
		}
		Teammate struct {
			ExpandedView, ChildViewBefore, ChildViewAfter, ChildViewAfterCreate string
			Global                                                              protocol.JSONObject
			Tools                                                               []string
			ParentConsumer                                                      struct {
				Tasks                 []protocol.JSONObject
				ExpandedView, Painted string
			}
		}
		Canceled struct {
			ExpandedView string
			Requests     []protocol.JSONObject
		}
		Caps struct{ Blocked, SecureReads []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Caps.Blocked) != 0 || len(source.Caps.SecureReads) != 0 || len(source.Workflow.Requests) != 2 {
		t.Fatal(err, string(output))
	}
	if source.Canceled.ExpandedView != "none" || len(source.Canceled.Requests) != 1 {
		t.Fatal("Original held Workflow cancellation ran a late direct task setter", string(output))
	}
	for _, role := range []string{"workflow", "in-process"} {
		t.Run(role, func(t *testing.T) {
			var posts atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.Method == http.MethodHead {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				body, _ := io.ReadAll(request.Body)
				switch posts.Add(1) {
				case 1:
					if !bytes.Contains(body, []byte(`"name":"TaskUpdate"`)) {
						t.Errorf("Actual %s catalog lost the real task tool: %s", role, body)
					}
					emitNativeTaskTool(w, "actual-role-task-update", "TaskUpdate", map[string]any{"taskId": "99", "status": "in_progress"})
				case 2:
					if !bytes.Contains(body, []byte("Task not found")) {
						t.Errorf("Actual %s did not execute the original failing TaskUpdate: %s", role, body)
					}
					if role == "in-process" {
						emitNativeTaskTool(w, "actual-teammate-create", "TaskCreate", map[string]any{"subject": "ACTUAL_TEAMMATE_SHARED_TASK", "description": "Original child write reaches mounted parent store"})
					} else {
						emitPlanFixture(w, 2, "", "ACTUAL_ROLE_TASK_DONE")
					}
				case 3:
					if role != "in-process" || !bytes.Contains(body, []byte("created successfully")) || bytes.Contains(body, []byte(`"is_error":true`)) {
						t.Errorf("Actual teammate TaskCreate failed: %s", body)
					}
					emitPlanFixture(w, 3, "", "ACTUAL_ROLE_TASK_DONE")
				case 4:
					if role != "in-process" || !bytes.Contains(body, []byte("ACTUAL_PARENT_NEXT_HUMAN")) {
						t.Errorf("Next actual parent query differs: %s", body)
					}
					emitPlanFixture(w, 4, "", "ACTUAL_PARENT_NEXT_DONE")
				default:
					t.Errorf("Unexpected %s task model request: %s", role, body)
				}
			}))
			defer peer.Close()
			parent := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
				delete(n.Environment, "CLAUDE_CODE_SIMPLE")
				n.Environment["CLAUDE_CODE_ENTRYPOINT"] = "cli"
				n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
				n.Environment["CLAUDE_CODE_ENABLE_TASKS"] = "1"
			}, func(o *MainPrintOptions) {
				o.Interactive = true
				o.Arguments.Tools = []string{"TaskUpdate,TaskCreate,TeamCreate"}
				o.Arguments.AgentsJSON = string(rawMain(agentsJSON))
			})
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := parent.SetTerminalExpandedView(ctx, "none"); err != nil {
				t.Fatal(err)
			}
			childView := ""
			var childViews []string
			if role == "workflow" {
				outcome, err := parent.runNativeWorkflowAgent(ctx, workflow.AgentExecution{Prompt: "Run the actual TaskUpdate for missing task 99", Options: protocol.NewJSONObject(fieldMain("agentType", "task-context-fixture")), RunID: "wf_task_callback-actual", OnAgentID: func(string) error { return nil }, OnProgress: func(workflow.AgentProgress) error { return nil }})
				if err != nil || string(outcome.Value) != `"ACTUAL_ROLE_TASK_DONE"` {
					t.Fatal("Actual workflow factory/Host failed", err, string(outcome.Value))
				}
			} else {
				callActualTeamTool(t, parent, "TeamCreate", `{"team_name":"task-source-team","description":"Actual shared task subscription"}`)
				changed := make(chan protocol.JSONObject, 64)
				_, remove, err := parent.SubscribeNativeTerminalTaskList(ctx, func(raw json.RawMessage) {
					select {
					case changed <- mainObj(raw):
					default:
						t.Error("Unbounded actual parent task publications")
					}
				})
				if err != nil {
					t.Fatal(err)
				}
				defer remove()
				var prepared nativeInProcessTurnPrepared
				if err := parent.Owner.Run(ctx, func(scope *cooperative.Scope) error {
					var err error
					prepared, err = parent.prepareNativeInProcessTurn(ctx, scope, nativeInProcessTurn{Identity: sessioncontroller.InProcessTeammateIdentity{TeamName: "task-source-team", AgentName: "worker", AgentID: "worker@task-source-team", ParentSessionID: parent.Controller.SessionID()}, Prompt: "Run actual TaskUpdate for missing99", PermissionMode: "default"})
					return err
				}); err != nil {
					t.Fatal(err)
				}
				defer prepared.ref.DeliveryFinished()
				options := prepared.options
				var child *MainPrint
				options.OnQueryYield = func(scope *cooperative.Scope, yield *queryloop.Yield) error {
					if yield.Message != nil && yield.Message.Type() == "user" {
						var err error
						childView, err = child.Controller.ExpandedViewOnOwner(scope)
						childViews = append(childViews, childView)
						return err
					}
					return nil
				}
				child, err = NewMainPrint(options)
				if err != nil {
					t.Fatal(err)
				}
				defer child.Close()
				if err = child.StartSession(ctx); err != nil {
					t.Fatal(err)
				}
				if err = child.RunTurn(ctx, protocol.NewJSONObject(fieldMain("type", "user"), fieldMain("message", map[string]any{"role": "user", "content": "Run actual TaskUpdate for missing99"}))); err != nil {
					t.Fatal(err)
				}
				watchCtx, stop := context.WithTimeout(ctx, 3*time.Second)
				defer stop()
				observed := false
				for !observed {
					select {
					case value := <-changed:
						var tasks []protocol.JSONObject
						_ = json.Unmarshal(value.Get("tasks"), &tasks)
						if len(tasks) == 1 && tasks[0].String("subject") == "ACTUAL_TEAMMATE_SHARED_TASK" {
							observed = true
							if value.Get("expandedView") != nil && value.String("expandedView") != source.Teammate.ParentConsumer.ExpandedView || len(source.Teammate.ParentConsumer.Tasks) != 1 || strings.Contains(source.Teammate.ParentConsumer.Painted, "ACTUAL_TEAMMATE_SHARED_TASK") {
								t.Error("Shared watcher implicitly expanded parent instead of preserving actual Source none", string(rawMain(value)), source.Teammate.ParentConsumer)
							}
						}
					case <-watchCtx.Done():
						t.Fatal("Actual child durable write did not reach parent singleton", watchCtx.Err())
					}
				}
				if err = parent.RunTurn(ctx, protocol.NewJSONObject(fieldMain("type", "user"), fieldMain("message", map[string]any{"role": "user", "content": "ACTUAL_PARENT_NEXT_HUMAN"}))); err != nil {
					t.Fatal(err)
				}
			}
			var view string
			if err := parent.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				var err error
				view, err = parent.Controller.ExpandedViewOnOwner(scope)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			want, expanded := source.Workflow.ExpandedView, nativeSettingsTruthy(source.Workflow.Global.Get("showExpandedTodos"))
			if role == "in-process" {
				want, expanded = source.Teammate.ExpandedView, nativeSettingsTruthy(source.Teammate.Global.Get("showExpandedTodos"))
				if len(childViews) != 2 || childViews[0] != source.Teammate.ChildViewAfter || childView != source.Teammate.ChildViewAfterCreate || source.Teammate.ChildViewBefore != "none" {
					t.Errorf("Actual async teammate child-own direct callbacks differ: Source%s->%s->%s Native%q", source.Teammate.ChildViewBefore, source.Teammate.ChildViewAfter, source.Teammate.ChildViewAfterCreate, childViews)
				}
			}
			global, err := parent.Global.Get()
			if err != nil {
				t.Fatal(err)
			}
			wantPosts := int32(2)
			if role == "in-process" {
				wantPosts = 4
			}
			if view != want || nativeSettingsTruthy(global.Get("showExpandedTodos")) != expanded || posts.Load() != wantPosts {
				t.Fatalf("Actual %s task parent callback differs: Source%s/global%v Native%s/global%v posts%d err%v", role, want, expanded, view, global.Get("showExpandedTodos"), posts.Load(), err)
			}
			if strings.Contains(string(rawMain(*global)), "task-source-team") {
				t.Fatal("Task role metadata leaked into persisted global config")
			}
		})
	}
}
