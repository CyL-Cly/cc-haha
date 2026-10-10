package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
)

// The Source observation replaces only physical worktree/child primitives. The
// Go half uses an actual private Git repository, actual MainPrint worker, HTTP
// model primitive and filesystem cleanup, including a rejected journal callback.
func TestActualSourceWorkflowIdentityBeforePhysicalWorktreeAndCallbackFailure(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []map[string]any{{"dirty": false}, {"dirty": true}, {"rejectIdentity": true}}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-workflow.fixture.ts")
	input, _ := json.Marshal(map[string]any{"homeDir": home, "workflowAgentOrder": rows})
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_WORKFLOW_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if e := command.Run(); e != nil {
		t.Fatalf("Source actual worker order %v %.1600s %.1600s", e, stdout.String(), stderr.String())
	}
	var source struct {
		WorkflowAgentOrder []protocol.JSONObject
		Capabilities       struct{ Blocked []string }
	}
	if e := json.Unmarshal(stdout.Bytes(), &source); e != nil || len(source.Capabilities.Blocked) > 0 || len(source.WorkflowAgentOrder) != len(rows) {
		t.Fatal(e, source.Capabilities.Blocked, stdout.String())
	}
	for i, row := range rows {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			f := worktreeMainFixture(t)
			_ = os.Remove(filepath.Join(f.cwd, ".worktreeinclude"))
			f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
			f.n.Environment["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
			var calls atomic.Int32
			var isolated string
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				if _, e := os.Stat(isolated); e != nil {
					t.Errorf("child ran before actual worktree creation %v", e)
				}
				if row["dirty"] == true && n == 1 {
					emitNativeTaskTool(w, "workflow-order-write", "Write", map[string]any{"file_path": filepath.Join(isolated, "proof.txt"), "content": "physical isolated output"})
				} else {
					emitPlanFixture(w, int(n), "", "actual-source-child")
				}
			}))
			defer peer.Close()
			f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
			disabled := false
			m, e := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "private actual Workflow parent", Tools: []string{"Read,Write"}, SessionID: "11111111-1111-4111-8111-111111111111", MaxTurns: 8, PermissionMode: "bypassPermissions", Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() {
				if e := m.ShutdownSession(context.Background()); e != nil {
					t.Error(e)
				}
			})
			if e = m.StartSession(ctx); e != nil {
				t.Fatal(e)
			}
			before := false
			idCalls := 0
			options := protocol.NewJSONObject(protocol.JSONField{Name: "isolation", Value: quoteMain("worktree")})
			_, e = m.runNativeWorkflowAgent(ctx, workflow.AgentExecution{Prompt: "actual isolated child", Options: options, RunID: "wf_order-abc", WorkflowName: "order", Index: 1, OnProgress: func(workflow.AgentProgress) error { return nil }, OnAgentID: func(id string) error {
				idCalls++
				isolated = filepath.Join(f.cwd, ".claude", "worktrees", "agent-"+id[:8])
				_, stat := os.Stat(isolated)
				before = os.IsNotExist(stat)
				// Real callback delivery can submit to the same parent Owner. This also
				// proves that fixing Source timing does not block the Owner event loop.
				delivery, e := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error { _, e := m.Controller.BackgroundTasksOnOwner(scope); return e })
				if e == nil {
					e = delivery.Wait()
				}
				if e != nil {
					return e
				}
				if row["rejectIdentity"] == true {
					return errors.New("identity journal rejected")
				}
				return nil
			}})
			actual := protocol.NewJSONObject(protocol.JSONField{Name: "beforeCreation", Value: rawMain(before)}, protocol.JSONField{Name: "idCalls", Value: rawMain(idCalls)}, protocol.JSONField{Name: "created", Value: rawMain(calls.Load() > 0)}, protocol.JSONField{Name: "removed", Value: rawMain(false)}, protocol.JSONField{Name: "modelCalls", Value: rawMain(min(1, calls.Load()))})
			if e != nil {
				actual.Set("error", quoteMain(e.Error()))
			}
			if calls.Load() > 0 {
				_, stat := os.Stat(isolated)
				actual.Set("removed", rawMain(os.IsNotExist(stat)))
			} else if isolated != "" {
				if _, stat := os.Stat(isolated); !os.IsNotExist(stat) {
					t.Fatal("callback failure created/leaked worktree", stat)
				}
			}
			if row["dirty"] == true {
				data, e := os.ReadFile(filepath.Join(isolated, "proof.txt"))
				if e != nil || string(data) != "physical isolated output" {
					t.Fatal(e, string(data))
				}
			}
			canonical := func(raw []byte) any { var v any; _ = json.Unmarshal(raw, &v); return v }
			if !reflect.DeepEqual(canonical(rawMain(actual)), canonical(rawMain(source.WorkflowAgentOrder[i]))) {
				t.Fatalf("actual Source order differs\nGo %s\nSource %s", rawMain(actual), rawMain(source.WorkflowAgentOrder[i]))
			}
		})
	}
}
