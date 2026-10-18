package clibootstrap

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

type nativeWorkflowRun struct {
	parent                                               *MainPrint
	taskID, runID, scriptPath, transcriptDir, outputPath string
	meta                                                 protocol.JSONObject
	args                                                 json.RawMessage
	ctx                                                  context.Context
	cancel                                               context.CancelCauseFunc
	done                                                 chan struct{}
	// Per-agent controls live within this unique task's callbacks, never in a
	// second task registry. Every mutation is delivered on the owning Controller.
	controllers   map[string]context.CancelCauseFunc
	pending       []protocol.JSONObject
	timer         *time.Timer
	lastPanelEmit float64
	mu            sync.Mutex
}

func nativeWorkflowRunID() (string, error) {
	bytes := make([]byte, 6)
	if _, e := rand.Read(bytes); e != nil {
		return "", e
	}
	text := hex.EncodeToString(bytes)
	return "wf_" + text[:8] + "-" + text[8:11], nil
}
func nativeWorkflowTaskID() (string, error) {
	bytes := make([]byte, 10)
	if _, e := rand.Read(bytes); e != nil {
		return "", e
	}
	return "w" + hex.EncodeToString(bytes)[:16], nil
}

func (m *MainPrint) launchNativeWorkflow(ctx context.Context, scope *cooperative.Scope, input protocol.JSONObject, toolID json.RawMessage) (protocol.JSONObject, error) {
	if !scope.BelongsTo(m.Owner) || m.workflowDefinitions == nil {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	script, path := input.String("script"), input.String("scriptPath")
	if path != "" {
		read, e := nativeSkillWork(m, scope, func() (string, error) {
			data, e := m.options.Native.readFile(path)
			return string(protocol.DecodeJavaScriptUTF8(data)), e
		})
		if e != nil {
			return protocol.JSONObject{}, e
		}
		script, e = read.Await(scope)
		if e != nil {
			return protocol.JSONObject{}, fmt.Errorf("Failed to read workflow script file %s: %s", path, e)
		}
	} else if name := input.String("name"); name != "" {
		var e error
		script, e = m.workflowDefinitions.NamedScriptOnOwner(scope, name)
		if e != nil {
			return protocol.JSONObject{}, e
		}
	}
	if script == "" {
		return protocol.JSONObject{}, errors.New("Workflow requires one of `script`, `scriptPath`, or `name`.")
	}
	prepared, e := m.workflowDefinitions.PrepareOnOwner(scope, quoteMain(script))
	if e != nil {
		return protocol.JSONObject{}, e
	}
	runID := input.String("resumeFromRunId")
	resume := input.Get("resumeFromRunId") != nil
	if !resume {
		runID, e = nativeWorkflowRunID()
		if e != nil {
			return protocol.JSONObject{}, e
		}
	}
	taskID, e := nativeWorkflowTaskID()
	if e != nil {
		return protocol.JSONObject{}, e
	}
	transcript, e := m.Controller.Writer().ComputedTranscriptPath(scope)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	directory := filepath.Dir(transcript)
	snapshot := m.state.Snapshot()
	if snapshot.SessionProjectDir != nil {
		directory = *snapshot.SessionProjectDir
	}
	transcriptDir := filepath.Join(directory, m.Controller.SessionID(), "subagents", "workflows", runID)
	callerPath := path != ""
	if !callerPath {
		name := regexp.MustCompile(`[^a-zA-Z0-9._-]`).ReplaceAllString(prepared.Meta.String("name"), "-")
		if len(name) > 64 {
			name = name[:64]
		}
		if name == "" {
			name = "workflow"
		}
		path = filepath.Join(directory, m.Controller.SessionID(), "workflows", name+"."+runID+".js")
	}
	outputDir, e := m.nativeTaskOutputDirectory(scope)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	outputPath := filepath.Join(outputDir, taskID+".output")
	for _, p := range []string{transcriptDir, outputPath, path} {
		if e := m.options.Native.check("workflow-artifact", p); e != nil {
			return protocol.JSONObject{}, e
		}
	}
	bindings := m.nativeBackgroundBindings()
	if resume {
		old, e := bindings.List(scope)
		if e != nil {
			return protocol.JSONObject{}, e
		}
		for _, task := range old {
			if task.String("type") == "local_workflow" && task.String("workflowRunId") == runID && task.String("status") != "running" {
				if e = bindings.Delete(scope, task.String("id")); e != nil {
					return protocol.JSONObject{}, e
				}
			}
		}
	}
	// Root workflow jobs outlive the completed human turn; nested agent-owned
	// workflows follow their owning Agent lifetime, like Source launchWorkflow.
	base := context.Background()
	if m.options.AgentID != "" {
		base = ctx
	}
	runCtx, cancel := context.WithCancelCause(base)
	model, e := m.Host.InferenceModel(scope)
	if e != nil {
		cancel(e)
		return protocol.JSONObject{}, e
	}
	params := protocol.NewJSONObject(protocol.JSONField{Name: "taskId", Value: quoteMain(taskID)}, protocol.JSONField{Name: "script", Value: quoteMain(script)}, protocol.JSONField{Name: "scriptPath", Value: quoteMain(path)}, protocol.JSONField{Name: "args", Value: input.Get("args")}, protocol.JSONField{Name: "summary", Value: prepared.Meta.Get("description")}, protocol.JSONField{Name: "workflowName", Value: prepared.Meta.Get("name")}, protocol.JSONField{Name: "title", Value: prepared.Meta.Get("title")}, protocol.JSONField{Name: "phases", Value: prepared.Meta.Get("phases")}, protocol.JSONField{Name: "defaultModel", Value: quoteMain(model)}, protocol.JSONField{Name: "workflowRunId", Value: quoteMain(runID)}, protocol.JSONField{Name: "toolUseId", Value: toolID})
	if m.options.AgentID != "" {
		params.Set("ownerAgentId", quoteMain(m.options.AgentID))
	}
	record := workflow.TaskRecord(params, m.options.Native.NowMillis(), outputPath)
	run := &nativeWorkflowRun{parent: m, taskID: taskID, runID: runID, scriptPath: path, transcriptDir: transcriptDir, outputPath: outputPath, meta: prepared.Meta.Clone(), args: append(json.RawMessage(nil), input.Get("args")...), ctx: runCtx, cancel: cancel, done: make(chan struct{}), controllers: map[string]context.CancelCauseFunc{}, pending: []protocol.JSONObject{}}
	entry := &sessioncontroller.BackgroundTask{Data: record, Cancel: cancel, ReadOutput: func(context.Context) (json.RawMessage, error) { return foregroundTaskRead(outputPath) }, Join: func(ctx context.Context) error {
		select {
		case <-run.done:
			return nil
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}, Stop: func(scope *cooperative.Scope, now float64) error { return run.stop(scope, now, "killed") }, WorkflowControl: run.control}
	if e = bindings.Register(scope, entry); e != nil {
		cancel(e)
		return protocol.JSONObject{}, e
	}
	// Original framework.registerTask publishes this start after registration,
	// before the workflow goroutine can enqueue any progress or terminal.
	started := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("system")}, protocol.JSONField{Name: "subtype", Value: quoteMain("task_started")}, protocol.JSONField{Name: "task_id", Value: record.Get("id")}, protocol.JSONField{Name: "tool_use_id", Value: record.Get("toolUseId")}, protocol.JSONField{Name: "description", Value: record.Get("description")}, protocol.JSONField{Name: "task_type", Value: record.Get("type")}, protocol.JSONField{Name: "workflow_name", Value: record.Get("workflowName")}, protocol.JSONField{Name: "workflow_run_id", Value: record.Get("workflowRunId")}, protocol.JSONField{Name: "prompt", Value: record.Get("prompt")})
	if record.String("ownerAgentId") != "" {
		started.Set("owner_agent_id", record.Get("ownerAgentId"))
	}
	if e = bindings.Emit(scope, started); e != nil {
		cancel(e)
		return protocol.JSONObject{}, e
	}
	binding, e := m.Controller.BorrowNativeCopyBinding(m.Owner)
	if e != nil {
		cancel(e)
		return protocol.JSONObject{}, e
	}
	ref, e := binding.Acquire(scope)
	if e != nil {
		cancel(e)
		return protocol.JSONObject{}, e
	}
	before, e := m.workflowOutputTokens(scope)
	if e != nil {
		ref.DeliveryFinished()
		cancel(e)
		return protocol.JSONObject{}, e
	}
	go func() {
		defer close(run.done)
		defer ref.DeliveryFinished()
		defer cancel(nil)
		if !callerPath {
			if e := os.MkdirAll(filepath.Dir(path), 0777); e == nil {
				_ = os.WriteFile(path, []byte(script), 0666)
			}
		}
		journal, e := workflow.NewJournal(filepath.Join(transcriptDir, "journal.jsonl"), workflow.NativeJournalFiles())
		var cached *workflow.JournalSnapshot
		if e == nil && resume {
			snapshot, e2 := journal.Load()
			e = e2
			cached = &snapshot
		}
		outcome := workflow.ExecutionOutcome{Result: []byte("null"), Logs: []string{}, Failures: []string{}}
		if e == nil {
			phases := []string{}
			var declarations []protocol.JSONObject
			_ = json.Unmarshal(prepared.Meta.Get("phases"), &declarations)
			for _, phase := range declarations {
				phases = append(phases, phase.String("title"))
			}
			spent := func() float64 {
				var value float64
				receipt, e := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error { var e error; value, e = m.workflowOutputTokens(scope); return e })
				if e == nil {
					e = receipt.Wait()
				}
				if e != nil {
					panic(e)
				}
				return value - before
			}
			outcome, e = workflow.Execute(runCtx, workflow.ExecutionOptions{Program: prepared.Program, RunID: runID, WorkflowName: prepared.Meta.String("name"), Model: model, Args: run.args, SeedPhaseTitles: phases, Now: m.options.Native.NowMillis, Concurrency: min(16, max(2, runtime.NumCPU()-2)), GetTurnSpent: spent, Journal: journal, JournalSnapshot: cached, RunAgent: m.runNativeWorkflowAgent, Emit: run.push, OnAgentController: run.controller, WorktreeUnavailableReason: m.nativeWorkflowIsolationReason, PrepareNested: m.prepareNativeNestedWorkflow})
		}
		if e != nil {
			outcome.Error = e.Error()
		}
		_ = run.flush()
		receipt, e := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error { return run.settle(scope, outcome) })
		if e == nil {
			_ = receipt.Wait()
		}
	}()
	return protocol.NewJSONObject(protocol.JSONField{Name: "status", Value: quoteMain("async_launched")}, protocol.JSONField{Name: "taskId", Value: quoteMain(taskID)}, protocol.JSONField{Name: "taskType", Value: quoteMain("local_workflow")}, protocol.JSONField{Name: "workflowName", Value: prepared.Meta.Get("name")}, protocol.JSONField{Name: "runId", Value: quoteMain(runID)}, protocol.JSONField{Name: "summary", Value: prepared.Meta.Get("description")}, protocol.JSONField{Name: "transcriptDir", Value: quoteMain(transcriptDir)}, protocol.JSONField{Name: "scriptPath", Value: quoteMain(path)}), nil
}
func (m *MainPrint) workflowOutputTokens(scope *cooperative.Scope) (float64, error) {
	ledger, e := m.Controller.ModelAccountingFor(m.Owner)
	if e != nil {
		return 0, e
	}
	snapshot, e := ledger.Snapshot(scope)
	if e != nil {
		return 0, e
	}
	var tokens float64
	e = json.Unmarshal(snapshot.Get("totalOutputTokens"), &tokens)
	return tokens, e
}
func (m *MainPrint) nativeWorkflowIsolationReason() (string, error) {
	var reason string
	receipt, e := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error {
		if e := m.hook.EmptyEvent("WorktreeCreate", m.Controller.SessionID()); e != nil {
			return e
		}
		if usercontext.GitRoot(mainSettingsFiles{n: m.options.Native}, m.workingDirectory()) == "" {
			reason = worktree.AgentUnavailableReason
		}
		return nil
	})
	if e == nil {
		e = receipt.Wait()
	}
	return reason, e
}
