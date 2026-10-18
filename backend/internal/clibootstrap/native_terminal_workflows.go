package clibootstrap

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
)

func (r *nativeWorkflowRun) control(scope *cooperative.Scope, action, key string) error {
	if !scope.BelongsTo(r.parent.Owner) {
		return cooperative.ErrRestricted
	}
	task, err := r.parent.Controller.BackgroundTaskOnOwner(scope, r.taskID)
	if err != nil {
		return err
	}
	if task == nil || task.Data.String("status") != "running" {
		return errors.New("Workflow is no longer running")
	}
	switch action {
	case "pause":
		return r.stop(scope, r.parent.options.Native.NowMillis(), "paused")
	case "skip", "retry":
		cancel, exists := r.controllers[key]
		if !exists {
			return errors.New("Workflow agent is no longer running")
		}
		cancel(errors.New("AbortError: user-" + action))
		return nil
	}
	return errors.New("Unknown workflow control")
}
func (m *MainPrint) NativeWorkflowsCommandEnabled() bool {
	if m.workflowDefinitions == nil || nativeEnvTruthy(m.options.Native.Environment["CLAUDE_CODE_DISABLE_WORKFLOWS"]) {
		return false
	}
	policy, err := m.Startup.GetSettingsForSource("policySettings")
	if err != nil || policy != nil && string(policy.Get("disableWorkflows")) == "true" {
		return false
	}
	initial, err := m.Startup.GetInitialSettings()
	return err == nil && (initial == nil || string(initial.Get("disableWorkflows")) != "true" && string(initial.Get("enableWorkflows")) != "false")
}
func (m *MainPrint) TerminalWorkflowControl(ctx context.Context, id, action, key string) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		task, err := m.Controller.BackgroundTaskOnOwner(scope, id)
		if err != nil {
			return err
		}
		if task == nil || task.Data.String("type") != "local_workflow" || task.WorkflowControl == nil {
			return errors.New("Workflow task is no longer available")
		}
		if err = task.WorkflowControl(scope, action, key); err != nil {
			return err
		}
		current, err := m.Controller.BackgroundTaskOnOwner(scope, id)
		if err != nil {
			return err
		}
		result = current.Data.Clone()
		if action == "pause" {
			result.Set("resumePrompt", quoteMain(workflow.ResumePrompt(task.Data)))
		}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}
func (m *MainPrint) SaveTerminalWorkflow(ctx context.Context, id, scopeName string) (protocol.JSONObject, error) {
	var task protocol.JSONObject
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		current, err := m.Controller.BackgroundTaskOnOwner(scope, id)
		if err != nil {
			return err
		}
		if current == nil || current.Data.String("type") != "local_workflow" {
			return errors.New("Workflow task is no longer available")
		}
		task = current.Data.Clone()
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	if err != nil {
		return protocol.JSONObject{}, err
	}
	result, err := m.workflowDefinitions.SaveDefinition(ctx, task.Get("script"), scopeName, nil, nil)
	if err == nil {
		_, err = m.ReloadComponents(ctx)
	}
	return result, err
}
