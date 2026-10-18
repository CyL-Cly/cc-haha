package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

func (m *NativeMCPToolContext) launchOrdinaryWorkflow(ctx context.Context, scope *cooperative.Scope, input protocol.JSONObject, toolID json.RawMessage) (protocol.JSONObject, error) {
	if err := m.checkOrdinary(scope); err != nil {
		return protocol.JSONObject{}, err
	}
	script, path := input.String("script"), input.String("scriptPath")
	if path != "" {
		resolvedPath := path
		if !filepath.IsAbs(resolvedPath) {
			resolvedPath = filepath.Join(m.state.Snapshot().CWD, resolvedPath)
		}
		future, err := nativeOwnedWork(m.command.Owner, m.binding, scope, func() ([]byte, error) { return m.command.Native.readFile(resolvedPath) })
		if err != nil {
			return protocol.JSONObject{}, err
		}
		data, err := future.Await(scope)
		if err != nil {
			return protocol.JSONObject{}, fmt.Errorf("Failed to read workflow script file %s: %s", path, ordinaryWorkflowFileError(err, path))
		}
		script = string(protocol.DecodeJavaScriptUTF8(data))
	} else if name := input.String("name"); name != "" {
		var err error
		script, err = m.workflowDefinitions.NamedScriptOnOwner(scope, name)
		if err != nil {
			return protocol.JSONObject{}, err
		}
	}
	if script == "" {
		return protocol.JSONObject{}, errors.New("Workflow requires one of `script`, `scriptPath`, or `name`.")
	}
	prepared, err := m.workflowDefinitions.PrepareOnOwner(scope, quoteMain(script))
	if err != nil {
		return protocol.JSONObject{}, err
	}
	runID := input.String("resumeFromRunId")
	if runID == "" {
		runID, err = nativeWorkflowRunID()
		if err != nil {
			return protocol.JSONObject{}, err
		}
	}
	taskID, err := nativeWorkflowTaskID()
	if err != nil {
		return protocol.JSONObject{}, err
	}
	n := m.command.Native
	config := n.ConfigHomeDirectory()
	if config == "" {
		config = filepath.Join(n.Home, ".claude")
	}
	directory := filepath.Join(config, "projects", projectpath.Sanitize(m.state.Snapshot().OriginalCWD))
	transcriptDir := filepath.Join(directory, m.state.Snapshot().SessionID, "subagents", "workflows", runID)
	callerPath := path != ""
	if !callerPath {
		name := regexp.MustCompile(`[^a-zA-Z0-9._-]`).ReplaceAllString(prepared.Meta.String("name"), "-")
		if len(name) > 64 {
			name = name[:64]
		}
		if name == "" {
			name = "workflow"
		}
		path = filepath.Join(directory, m.state.Snapshot().SessionID, "workflows", name+"."+runID+".js")
	}
	for _, file := range []string{transcriptDir, path} {
		if err := n.check("MCP workflow artifact", file); err != nil {
			return protocol.JSONObject{}, err
		}
	}
	lease, err := m.binding.Acquire(scope)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	args := append([]byte(nil), input.Get("args")...)
	model := m.model
	resume := input.Get("resumeFromRunId") != nil
	meta := prepared.Meta.Clone()
	var declarations []protocol.JSONObject
	_ = json.Unmarshal(meta.Get("phases"), &declarations)
	phases := []string{}
	for _, p := range declarations {
		phases = append(phases, p.String("title"))
	}
	before, e := m.models.OutputTokens(scope)
	if e != nil {
		lease.DeliveryFinished()
		return protocol.JSONObject{}, e
	}
	go func() {
		defer lease.DeliveryFinished()
		if !callerPath {
			if err := os.MkdirAll(filepath.Dir(path), 0777); err == nil {
				_ = os.WriteFile(path, []byte(script), 0666)
			}
		}
		journal, err := workflow.NewJournal(filepath.Join(transcriptDir, "journal.jsonl"), workflow.NativeJournalFiles())
		if err != nil {
			return
		}
		var cached *workflow.JournalSnapshot
		if resume {
			snapshot, _ := journal.Load()
			cached = &snapshot
		}
		spent := func() float64 {
			value, e := ordinaryWorkflowOwner(m, context.Background(), m.models.OutputTokens)
			if e != nil {
				panic(e)
			}
			return value - before
		}
		_, _ = workflow.Execute(context.Background(), workflow.ExecutionOptions{Program: prepared.Program, RunID: runID, WorkflowName: meta.String("name"), Model: model, Args: args, SeedPhaseTitles: phases, Now: n.NowMillis, Concurrency: min(16, max(2, runtime.NumCPU()-2)), Journal: journal, JournalSnapshot: cached, GetTurnSpent: spent, RunAgent: m.runOrdinaryWorkflowAgent, PrepareNested: m.prepareOrdinaryNestedWorkflow, WorktreeUnavailableReason: func() (string, error) {
			has, e := m.hooks.OutsideEventConfigured("WorktreeCreate", m.state.Snapshot().SessionID)
			if e != nil {
				return "", e
			}
			if !has && usercontext.GitRoot(mainSettingsFiles{n: n}, n.CWD) == "" {
				return worktree.AgentUnavailableReason, nil
			}
			return "", nil
		}, Emit: func(protocol.JSONObject) error { return nil }})
		done, flushError := journal.PendingFlush()
		<-done
		_ = flushError()
	}()
	return protocol.NewJSONObject(protocol.JSONField{Name: "status", Value: quoteMain("async_launched")}, protocol.JSONField{Name: "taskId", Value: quoteMain(taskID)}, protocol.JSONField{Name: "taskType", Value: quoteMain("local_workflow")}, protocol.JSONField{Name: "workflowName", Value: prepared.Meta.Get("name")}, protocol.JSONField{Name: "runId", Value: quoteMain(runID)}, protocol.JSONField{Name: "summary", Value: prepared.Meta.Get("description")}, protocol.JSONField{Name: "transcriptDir", Value: quoteMain(transcriptDir)}, protocol.JSONField{Name: "scriptPath", Value: quoteMain(path)}), nil
}

func ordinaryWorkflowFileError(err error, path string) string {
	if native, ok := err.(*os.PathError); ok {
		copy := *native
		copy.Path = path
		err = &copy
	}
	return strings.TrimPrefix(storage.JavaScriptErrorText(err), "Error: ")
}
