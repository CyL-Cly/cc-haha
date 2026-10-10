package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
)

func (r *nativeWorkflowRun) onOwner(fn func(*cooperative.Scope) error) error {
	receipt, e := r.parent.Owner.SubmitIndependent(context.Background(), cooperative.Read, fn)
	if e == nil {
		e = receipt.Wait()
	}
	return e
}
func (r *nativeWorkflowRun) controller(key string, cancel context.CancelCauseFunc) error {
	return r.onOwner(func(scope *cooperative.Scope) error {
		task, e := r.parent.nativeBackgroundBindings().Lookup(scope, r.taskID)
		if e != nil {
			return e
		}
		if task == nil || task.Data.String("status") != "running" {
			return nil
		}
		if cancel == nil {
			delete(r.controllers, key)
		} else {
			r.controllers[key] = cancel
		}
		return nil
	})
}
func (r *nativeWorkflowRun) push(event protocol.JSONObject) error {
	r.mu.Lock()
	r.pending = append(r.pending, event.Clone())
	if r.timer == nil {
		r.timer = time.AfterFunc(16*time.Millisecond, func() { _ = r.flush() })
	}
	r.mu.Unlock()
	return nil
}
func (r *nativeWorkflowRun) flush() error {
	r.mu.Lock()
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	batch := r.pending
	r.pending = []protocol.JSONObject{}
	r.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	return r.onOwner(func(scope *cooperative.Scope) error {
		b := r.parent.nativeBackgroundBindings()
		if e := b.Update(scope, r.taskID, func(task protocol.JSONObject) (protocol.JSONObject, error) {
			return workflow.ApplyProgressBatch(task, batch)
		}); e != nil {
			return e
		}
		task, e := b.Lookup(scope, r.taskID)
		if e != nil {
			return e
		}
		if task == nil || task.Data.String("status") != "running" {
			return nil
		}
		durable := []protocol.JSONObject{}
		onlyChurn := true
		var last protocol.JSONObject
		for _, event := range batch {
			if event.String("type") == "workflow_log" {
				continue
			}
			durable = append(durable, event)
			if event.String("type") == "workflow_agent" {
				last = event.Clone()
			}
			if event.String("type") != "workflow_agent" || event.String("state") != "progress" {
				onlyChurn = false
			}
		}
		if len(durable) == 0 {
			return nil
		}
		now := r.parent.options.Native.NowMillis()
		if onlyChurn && now-r.lastPanelEmit < 10000 {
			return nil
		}
		r.lastPanelEmit = now
		description := task.Data.Get("description")
		if last.IsObject() {
			description = last.Get("label")
			if phase := last.String("phaseTitle"); phase != "" {
				description = quoteMain(phase + ": " + last.String("label"))
			}
		}
		var all []protocol.JSONObject
		_ = json.Unmarshal(task.Data.Get("workflowProgress"), &all)
		rows := []protocol.JSONObject{}
		for _, event := range all {
			if event.String("type") != "workflow_log" {
				rows = append(rows, event)
			}
		}
		event := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("system")}, protocol.JSONField{Name: "subtype", Value: quoteMain("task_progress")}, protocol.JSONField{Name: "task_id", Value: quoteMain(r.taskID)}, protocol.JSONField{Name: "tool_use_id", Value: task.Data.Get("toolUseId")}, protocol.JSONField{Name: "description", Value: description}, protocol.JSONField{Name: "usage", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "total_tokens", Value: task.Data.Get("totalTokens")}, protocol.JSONField{Name: "tool_uses", Value: task.Data.Get("totalToolCalls")}, protocol.JSONField{Name: "duration_ms", Value: rawMain(now - jsonNumberNative(task.Data.Get("startTime")))}))}, protocol.JSONField{Name: "summary", Value: task.Data.Get("summary")}, protocol.JSONField{Name: "workflow_run_id", Value: task.Data.Get("workflowRunId")}, protocol.JSONField{Name: "workflow_progress", Value: rawMain(rows)}, protocol.JSONField{Name: "owner_agent_id", Value: task.Data.Get("ownerAgentId")})
		if last.IsObject() {
			event.Set("last_tool_name", last.Get("label"))
		}
		return b.Emit(scope, event)
	})
}
func jsonNumberNative(raw json.RawMessage) float64 {
	var value float64
	_ = json.Unmarshal(raw, &value)
	return value
}
func (r *nativeWorkflowRun) stop(scope *cooperative.Scope, now float64, status string) error {
	b := r.parent.nativeBackgroundBindings()
	task, e := b.Lookup(scope, r.taskID)
	if e != nil {
		return e
	}
	if task == nil || task.Data.String("status") != "running" {
		return nil
	}
	r.cancel(context.Canceled)
	for key, cancel := range r.controllers {
		cancel(context.Canceled)
		delete(r.controllers, key)
	}
	if e = b.Update(scope, r.taskID, func(data protocol.JSONObject) (protocol.JSONObject, error) {
		data.Set("status", quoteMain(status))
		data.Set("endTime", rawMain(now))
		if status == "paused" {
			data.Set("notified", []byte("true"))
		} else {
			data.Set("evictAfter", rawMain(now+30000))
		}
		return data, nil
	}); e != nil {
		return e
	}
	if status == "paused" {
		return nil
	}
	return r.notify(scope, status, workflow.ExecutionOutcome{AgentCount: int(jsonNumberNative(task.Data.Get("agentCount"))), DurationMillis: now - jsonNumberNative(task.Data.Get("startTime"))}, task.Data)
}
func (r *nativeWorkflowRun) settle(scope *cooperative.Scope, outcome workflow.ExecutionOutcome) error {
	b := r.parent.nativeBackgroundBindings()
	task, e := b.Lookup(scope, r.taskID)
	if e != nil {
		return e
	}
	if task == nil || task.Data.String("status") != "running" {
		return nil
	}
	status := "completed"
	if outcome.Error != "" {
		status = "failed"
	}
	now := r.parent.options.Native.NowMillis()
	r.cancel(nil)
	for key := range r.controllers {
		delete(r.controllers, key)
	}
	if e = b.Update(scope, r.taskID, func(data protocol.JSONObject) (protocol.JSONObject, error) {
		data.Set("status", quoteMain(status))
		data.Set("endTime", rawMain(now))
		data.Set("evictAfter", rawMain(now+30000))
		data.Set("agentCount", rawMain(outcome.AgentCount))
		data.Set("logs", rawMain(outcome.Logs))
		if outcome.Error != "" {
			data.Set("error", quoteMain(outcome.Error))
		} else {
			data.Set("result", outcome.Result)
		}
		return data, nil
	}); e != nil {
		return e
	}
	rows := []protocol.JSONObject{}
	var progress []protocol.JSONObject
	_ = json.Unmarshal(task.Data.Get("workflowProgress"), &progress)
	for _, row := range progress {
		if row.String("type") != "workflow_log" {
			rows = append(rows, row)
		}
	}
	output := protocol.NewJSONObject(protocol.JSONField{Name: "summary", Value: task.Data.Get("summary")}, protocol.JSONField{Name: "workflowName", Value: task.Data.Get("workflowName")}, protocol.JSONField{Name: "workflowRunId", Value: task.Data.Get("workflowRunId")}, protocol.JSONField{Name: "agentCount", Value: rawMain(outcome.AgentCount)}, protocol.JSONField{Name: "logs", Value: rawMain(outcome.Logs)}, protocol.JSONField{Name: "workflowProgress", Value: rawMain(rows)}, protocol.JSONField{Name: "totalTokens", Value: task.Data.Get("totalTokens")}, protocol.JSONField{Name: "totalToolCalls", Value: task.Data.Get("totalToolCalls")})
	if outcome.Error != "" {
		output.Set("error", quoteMain(outcome.Error))
	} else {
		output.Set("result", outcome.Result)
	}
	// Preserve original output field order while emitting Source's two-space JSON.
	ordered := protocol.NewJSONObject()
	for _, key := range []string{"summary", "workflowName", "workflowRunId", "agentCount", "logs", "result", "error", "workflowProgress", "totalTokens", "totalToolCalls"} {
		ordered.Set(key, output.Get(key))
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if e = encoder.Encode(ordered); e != nil {
		return e
	}
	data := bytes.TrimSuffix(buffer.Bytes(), []byte("\n"))
	write, e := nativeSkillWork(r.parent, scope, func() (struct{}, error) {
		if e := os.MkdirAll(filepath.Dir(r.outputPath), 0777); e != nil {
			return struct{}{}, e
		}
		if e := r.parent.options.Native.check("workflow-task-output", r.outputPath); e != nil {
			return struct{}{}, e
		}
		return struct{}{}, writeNativeWorkflowOutput(r.outputPath, data)
	})
	if e != nil {
		return e
	}
	_, _ = write.Await(scope)
	return r.notify(scope, status, outcome, task.Data)
}
func (r *nativeWorkflowRun) notify(scope *cooperative.Scope, status string, outcome workflow.ExecutionOutcome, task protocol.JSONObject) error {
	b := r.parent.nativeBackgroundBindings()
	current, e := b.Lookup(scope, r.taskID)
	if e != nil {
		return e
	}
	if current == nil || string(current.Data.Get("notified")) == "true" {
		return nil
	}
	if e = b.MarkNotified(scope, r.taskID); e != nil {
		return e
	}
	params := protocol.NewJSONObject(protocol.JSONField{Name: "taskId", Value: quoteMain(r.taskID)}, protocol.JSONField{Name: "summary", Value: task.Get("summary")}, protocol.JSONField{Name: "status", Value: quoteMain(status)}, protocol.JSONField{Name: "agentCount", Value: rawMain(outcome.AgentCount)}, protocol.JSONField{Name: "totalTokens", Value: task.Get("totalTokens")}, protocol.JSONField{Name: "totalToolCalls", Value: task.Get("totalToolCalls")}, protocol.JSONField{Name: "durationMs", Value: rawMain(outcome.DurationMillis)}, protocol.JSONField{Name: "toolUseId", Value: task.Get("toolUseId")}, protocol.JSONField{Name: "transcriptDir", Value: quoteMain(r.transcriptDir)}, protocol.JSONField{Name: "scriptPath", Value: quoteMain(r.scriptPath)}, protocol.JSONField{Name: "workflowRunId", Value: quoteMain(r.runID)}, protocol.JSONField{Name: "args", Value: r.args})
	if status == "completed" {
		params.Set("result", outcome.Result)
	}
	if outcome.Error != "" {
		params.Set("error", quoteMain(outcome.Error))
	}
	if len(outcome.Failures) > 0 {
		params.Set("failures", rawMain(outcome.Failures))
	}
	message, e := workflow.Notification(params, r.outputPath)
	if e != nil {
		return e
	}
	if e = b.Notify(scope, r.taskID, message); e != nil {
		return e
	}
	// Root bookends are emitted when the shared print consumer drains this
	// actual notification. Owned workflows need their explicit SDK bookend.
	if task.String("ownerAgentId") == "" {
		return nil
	}
	sdkStatus := status
	if status == "killed" {
		sdkStatus = "stopped"
	}
	event := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("system")}, protocol.JSONField{Name: "subtype", Value: quoteMain("task_notification")}, protocol.JSONField{Name: "task_id", Value: quoteMain(r.taskID)}, protocol.JSONField{Name: "tool_use_id", Value: task.Get("toolUseId")}, protocol.JSONField{Name: "status", Value: quoteMain(sdkStatus)}, protocol.JSONField{Name: "output_file", Value: quoteMain(r.outputPath)}, protocol.JSONField{Name: "summary", Value: task.Get("summary")}, protocol.JSONField{Name: "owner_agent_id", Value: task.Get("ownerAgentId")}, protocol.JSONField{Name: "usage", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "total_tokens", Value: task.Get("totalTokens")}, protocol.JSONField{Name: "tool_uses", Value: task.Get("totalToolCalls")}, protocol.JSONField{Name: "duration_ms", Value: rawMain(outcome.DurationMillis)}))})
	return b.Emit(scope, event)
}
