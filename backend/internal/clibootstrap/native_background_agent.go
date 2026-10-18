package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

// Background execution opens the existing native MainPrint harness on its own
// Owner. The parent retains the only task map, command queue and cumulative
// accounting authority; the child owns its actual sidechain and request loop.
func (m *MainPrint) runBackgroundAgent(ctx context.Context, scope *cooperative.Scope, request builtintools.AgentRequest) (*ownerasync.OwnerFuture[builtintools.AgentRunResult], error) {
	return m.startNativeBackgroundAgent(ctx, scope, request, nil)
}
func (m *MainPrint) startNativeBackgroundAgent(ctx context.Context, scope *cooperative.Scope, request builtintools.AgentRequest, restart *nativeAgentBackgroundRestart) (*ownerasync.OwnerFuture[builtintools.AgentRunResult], error) {
	if !scope.BelongsTo(m.Owner) || m.nativeModelSelector == nil || m.prompt == nil {
		return nil, cooperative.ErrRestricted
	}
	var releaseSlot func()
	var e error
	if restart != nil && restart.releaseSlot != nil {
		releaseSlot = restart.releaseSlot
	} else {
		releaseSlot, e = m.reserveOrdinarySubagent()
	}
	if e != nil {
		return nil, e
	}
	transferred := false
	defer func() {
		if !transferred {
			releaseSlot()
		}
	}()
	agentID, e := nativeAgentID()
	if e != nil {
		return nil, e
	}
	if request.ResumeAgentID != "" {
		agentID = request.ResumeAgentID
	}
	var worktreeRun *nativeAgentWorktree
	if restart != nil {
		worktreeRun = restart.worktree
	} else if request.ResumeAgentID == "" {
		worktreeRun, e = m.prepareAgentWorktree(ctx, scope, request, agentID)
	} else {
		worktreeRun = &nativeAgentWorktree{CWD: m.workingDirectory()}
		if request.ResumedWorktreePath != "" {
			worktreeRun.CWD = request.ResumedWorktreePath
			worktreeRun.Info = &worktree.AgentWorktree{Path: request.ResumedWorktreePath, HookBased: true}
		}
	}
	if e != nil {
		return nil, e
	}
	fail := func(e error) (*ownerasync.OwnerFuture[builtintools.AgentRunResult], error) {
		_, _ = m.finishAgentWorktree(scope, worktreeRun)
		return nil, e
	}
	parentModel := request.ParentModel
	if parentModel == "" {
		parentModel, e = m.Host.InferenceModel(scope)
		if e != nil {
			return fail(e)
		}
	}
	var toolModel, definitionModel json.RawMessage
	if request.Model != "" {
		toolModel = quoteMain(request.Model)
	}
	if request.DefinitionModel != "" {
		definitionModel = quoteMain(request.DefinitionModel)
	}
	parentMode := request.ParentPermissionMode
	if parentMode == "" {
		parentMode = request.PermissionContext.String("mode")
	}
	selected, e := m.nativeModelSelector.AgentModel(modelselection.AgentInput{AgentModel: definitionModel, ParentModel: quoteMain(parentModel), ToolSpecifiedModel: toolModel, PermissionMode: parentMode})
	if e != nil {
		return fail(e)
	}
	model, e := decodeNativeModel(selected)
	if e != nil {
		return fail(e)
	}
	sections := cloneAgentRawRows(request.SystemPromptSections)
	if len(sections) == 0 {
		return fail(mainCap("Agent original prompt sections"))
	}
	environment, e := m.prompt.AgentEnvironmentAt(ctx, model, nil, worktreeRun.CWD, func(context.Context) (bool, error) {
		return usercontext.GitRoot(mainSettingsFiles{n: m.options.Native}, worktreeRun.CWD) != "", nil
	})
	if e != nil {
		return fail(e)
	}
	sections = append(sections, environment)
	directory, e := m.nativeTaskOutputDirectory(scope)
	if e != nil {
		return fail(e)
	}
	var restartPrompt *sdkhost.NativeAgentRestartPrompt
	if restart != nil {
		restartPrompt = restart.prompt
	}
	o, e := m.nativeIndependentAgentOptions(scope, request, agentID, worktreeRun.CWD, model, directory, sections, restartPrompt)
	if e != nil {
		return fail(e)
	}
	// This factory is the original ordinary async Agent branch. Expose that
	// committed mode before the child assembles its first actual tool catalog,
	// using the same Source background filter as a promoted foreground child.
	o.DetachedAgent.agentSummary = request.AgentToolInvocation && m.nativeSDKAgentSummaryEnabled.Load()
	o.DetachedAgent.isBackgrounded = func() bool { return true }
	snapshot := m.state.Snapshot()
	progress := m.nativeAgentProgress(request, worktreeRun.CWD)
	streamed := []queryloop.Message{}
	if restart != nil {
		streamed = append(streamed, restart.messages...)
	}
	var child *MainPrint
	start := m.options.Native.NowMillis()
	if restart != nil {
		start = restart.start
		for _, row := range restart.messages {
			data, _ := row.MarshalJSON()
			progress.observe(data)
		}
	}
	o.OnQueryYield = func(scope *cooperative.Scope, yield *queryloop.Yield) error {
		if yield.Message == nil {
			return nil
		}
		data, e := yield.Message.MarshalJSON()
		if e != nil {
			return e
		}
		var displayed protocol.JSONObject
		if e = displayed.UnmarshalJSON(data); e != nil {
			return e
		}
		streamed = append(streamed, queryloop.Message{Record: displayed.Clone()})
		if e = builtintools.AppendRetainedAgentMessage(child.nativeBackgroundBindings(), scope, agentID, displayed); e != nil {
			return e
		}
		lastTool := progress.observe(data)
		if progress.failure != nil {
			return progress.failure
		}
		if e := child.updateNativeAgentProgress(scope, agentID, progress); e != nil {
			return e
		}
		if lastTool != "" {
			return child.emitForegroundAgentProgress(scope, agentID, request, start, progress, lastTool)
		}
		return nil
	}
	type initializedChild struct {
		process *MainPrint
		path    string
	}
	startup, e := nativeSkillWork(m, scope, func() (initializedChild, error) {
		var e error
		child, e = NewMainPrint(o)
		if e != nil {
			return initializedChild{}, e
		}
		if e = child.StartSession(context.Background()); e != nil {
			child.Close()
			return initializedChild{}, e
		}
		var path string
		if e = child.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			if e := child.installNativeIndependentAgentSkills(ctx, scope); e != nil {
				return e
			}
			if request.ResumeHistory != nil {
				if e := child.Controller.InstallAgentResumeHistory(scope, request.ResumeHistory); e != nil {
					return e
				}
			}
			var e error
			path, e = child.Controller.Writer().ComputedTranscriptPath(scope)
			return e
		}); e != nil {
			_ = child.ShutdownSession(context.Background())
			return initializedChild{}, e
		}
		return initializedChild{child, path}, nil
	})
	if e != nil {
		return fail(e)
	}
	initialized, e := startup.Await(scope)
	if e != nil {
		return fail(e)
	}
	child, path := initialized.process, initialized.path
	metadata := nativeAgentMetadata(request, worktreeRun, m.options.AgentID, restart != nil)
	m.writeAgentWorktreeMetadata(path, metadata)
	// Root tasks are independent of the completed human turn. An agent-owned
	// child follows that owning agent's cancellation, matching Source.
	base := context.Background()
	if m.options.AgentID != "" {
		base = ctx
	}
	if restart != nil && restart.abortContext != nil {
		base = restart.abortContext
	}
	agentContext, cancel := context.WithCancelCause(base)
	finished := make(chan struct{})
	// Until transfer, no goroutine owns this Join signal. All startup failures
	// settle it without fabricating a completed task or notification.
	defer func() {
		if !transferred {
			close(finished)
		}
	}()
	registration, e := m.prepareNativeAgentTask(scope, agentID, path, request, cancel, finished, true)
	abort := func(e error) (*ownerasync.OwnerFuture[builtintools.AgentRunResult], error) {
		cancel(e)
		if registration != nil {
			e = errors.Join(e, registration.Rollback(scope))
		}
		_ = child.ShutdownSession(context.Background())
		return fail(e)
	}
	if e != nil {
		return abort(e)
	}
	binding, e := m.Controller.BorrowNativeCopyBinding(m.Owner)
	if e != nil {
		return abort(e)
	}
	ref, e := binding.Acquire(scope)
	if e != nil {
		return abort(e)
	}
	registration.Commit()
	transferred = true
	go func() {
		defer releaseSlot()
		var run builtintools.AgentRunResult
		failure := child.Owner.Run(agentContext, func(scope *cooperative.Scope) error {
			input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "session_id", Value: quoteMain(snapshot.SessionID)}, protocol.JSONField{Name: "message", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoteMain("user")}, protocol.JSONField{Name: "content", Value: quoteMain(request.Prompt)}))})
			if e := child.Host.RunOne(scope, agentContext, input); e != nil {
				return e
			}
			if child.Host.ExitCode() != 0 {
				return fmt.Errorf("Agent %s query failed", agentID)
			}
			return nil
		})
		if e := child.Host.CloseAgentSummaries(context.Background()); failure == nil {
			failure = e
		}
		run = builtintools.AgentRunResult{AgentID: agentID, Messages: streamed, DurationMillis: child.options.Native.NowMillis() - start}
		// Source completes and notifies before optional worktree cleanup. The
		// original child lifetime lease continues to own and join that cleanup.
		completion, e := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error {
			return m.completeNativeBackgroundAgent(scope, agentID, request, run, failure, progress, start, nil)
		})
		if e == nil {
			e = completion.Wait()
		}
		_ = child.Owner.Run(context.Background(), func(scope *cooperative.Scope) error { return child.killAgentOwnedShells(scope, agentID) })
		_ = child.ShutdownSession(context.Background())
		receipt, e := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error {
			defer close(finished)
			defer ref.DeliveryFinished()
			retained, _ := m.finishAgentWorktree(scope, worktreeRun)
			if retained == nil && metadata.Get("worktreePath") != nil {
				metadata.Delete("worktreePath")
				m.writeAgentWorktreeMetadata(path, metadata)
			}
			return nil
		})
		if e == nil {
			e = receipt.Wait()
		}
		if e != nil {
			ref.DeliveryFinished()
			select {
			case <-finished:
			default:
				close(finished)
			}
		}
		cancel(nil)
	}()
	launched, e := ownerasync.NewOwnerFuture[builtintools.AgentRunResult](m.Owner)
	if e != nil {
		return nil, e
	}
	e = launched.Resolve(scope, builtintools.AgentRunResult{AgentID: agentID, Async: true, OutputFile: filepath.Join(directory, agentID+".output")})
	return launched, e
}

func (m *MainPrint) completeNativeBackgroundAgent(scope *cooperative.Scope, id string, request builtintools.AgentRequest, run builtintools.AgentRunResult, cause error, progress *foregroundAgentProgress, start float64, retained *worktree.AgentWorktree) error {
	b := m.nativeBackgroundBindings()
	entry, e := b.Lookup(scope, id)
	if e != nil || entry == nil {
		return e
	}
	status := "completed"
	if cause != nil {
		status = "failed"
	}
	if errors.Is(cause, context.Canceled) {
		status = "killed"
	}
	if entry.Data.String("status") == "killed" {
		status = "killed"
	}
	var result protocol.JSONObject
	if cause == nil {
		data, e := builtintools.FinalizeAgentRun(request, run)
		if e != nil {
			cause = e
			status = "failed"
		} else {
			_ = result.UnmarshalJSON(data)
			result.Delete("status")
			result.Delete("prompt")
		}
	}
	if e = b.Update(scope, id, func(data protocol.JSONObject) (protocol.JSONObject, error) {
		if data.String("status") == "running" {
			data.Set("status", quoteMain(status))
			data.Set("endTime", rawMain(m.options.Native.NowMillis()))
			data.Delete("selectedAgent")
			if string(data.Get("retain")) != "true" {
				data.Set("evictAfter", rawMain(m.options.Native.NowMillis()+30000))
			}
			if result.IsObject() {
				data.Set("result", rawMain(result))
			}
			if cause != nil && status == "failed" {
				data.Set("error", quoteMain(cause.Error()))
			}
		}
		return data, nil
	}); e != nil {
		return e
	}
	entry, e = b.Lookup(scope, id)
	if e != nil || entry == nil || string(entry.Data.Get("notified")) == "true" {
		return e
	}
	if e = b.MarkNotified(scope, id); e != nil {
		return e
	}
	summary := "Agent \"" + request.Description + "\" completed"
	if status == "failed" {
		message := "Unknown error"
		if cause != nil && cause.Error() != "" {
			message = cause.Error()
		}
		summary = "Agent \"" + request.Description + "\" failed: " + message
	}
	if status == "killed" {
		summary = "Agent \"" + request.Description + "\" was stopped"
	}
	output := entry.Data.String("outputFile")
	toolLine := ""
	if request.ToolUseID != nil && string(request.ToolUseID) != `""` {
		toolLine = "\n<tool-use-id>" + stringValueMain(request.ToolUseID) + "</tool-use-id>"
	}
	finalText := ""
	var blocks []protocol.JSONObject
	_ = json.Unmarshal(result.Get("content"), &blocks)
	for _, block := range blocks {
		if block.String("type") == "text" {
			if finalText != "" {
				finalText += "\n"
			}
			finalText += block.String("text")
		}
	}
	if status == "killed" {
		finalText = partialAgentText(run.Messages)
	}
	resultSection := ""
	if finalText != "" {
		resultSection = "\n<result>" + finalText + "</result>"
	}
	usageSection := ""
	var usage protocol.JSONObject
	if status == "completed" {
		usage = protocol.NewJSONObject(protocol.JSONField{Name: "total_tokens", Value: rawMain(progress.input + progress.output)}, protocol.JSONField{Name: "tool_uses", Value: result.Get("totalToolUseCount")}, protocol.JSONField{Name: "duration_ms", Value: result.Get("totalDurationMs")})
		usageSection = fmt.Sprintf("\n<usage><total_tokens>%s</total_tokens><tool_uses>%s</tool_uses><duration_ms>%s</duration_ms></usage>", usage.Get("total_tokens"), usage.Get("tool_uses"), usage.Get("duration_ms"))
	}
	worktreeSection := ""
	if retained != nil {
		worktreeSection = "\n<worktree><worktreePath>" + retained.Path + "</worktreePath>"
		if retained.Branch != "" {
			worktreeSection += "<worktreeBranch>" + retained.Branch + "</worktreeBranch>"
		}
		worktreeSection += "</worktree>"
	}
	message := "<task-notification>\n<task-id>" + id + "</task-id>" + toolLine + "\n<task-type>local_agent</task-type>\n<output-file>" + output + "</output-file>\n<status>" + status + "</status>\n<summary>" + summary + "</summary>" + resultSection + usageSection + worktreeSection + "\n</task-notification>"
	if request.OwnerAgentID != "" && b.NotifyForAgent != nil {
		e = b.NotifyForAgent(scope, request.OwnerAgentID, message)
	} else {
		e = b.Notify(scope, id, message)
	}
	if e != nil {
		return e
	}
	if request.OwnerAgentID == "" && m.options.AgentID == "" && b.RootNotificationsUseQueue != nil && b.RootNotificationsUseQueue(scope) {
		return nil
	}
	sdkStatus := status
	if status == "killed" {
		sdkStatus = "stopped"
	}
	event := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("system")}, protocol.JSONField{Name: "subtype", Value: quoteMain("task_notification")}, protocol.JSONField{Name: "task_id", Value: quoteMain(id)}, protocol.JSONField{Name: "status", Value: quoteMain(sdkStatus)}, protocol.JSONField{Name: "output_file", Value: quoteMain(output)}, protocol.JSONField{Name: "summary", Value: quoteMain(summary)})
	if request.ToolUseID != nil {
		event.Set("tool_use_id", request.ToolUseID)
	}
	if usage.IsObject() {
		event.Set("usage", rawMain(usage))
	}
	if request.OwnerAgentID != "" || m.options.AgentID != "" {
		owner := request.OwnerAgentID
		if owner == "" {
			owner = m.options.AgentID
		}
		event.Set("owner_agent_id", quoteMain(owner))
	} else if finalText != "" {
		event.Set("result", quoteMain(finalText))
	}
	return b.Emit(scope, event)
}

func partialAgentText(messages []queryloop.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		row := messages[i]
		if row.Type() != "assistant" {
			continue
		}
		data, e := row.MarshalJSON()
		if e != nil {
			continue
		}
		var envelope protocol.JSONObject
		_ = envelope.UnmarshalJSON(data)
		api, _ := envelope.Object("message")
		var blocks []protocol.JSONObject
		_ = json.Unmarshal(api.Get("content"), &blocks)
		texts := []string{}
		for _, block := range blocks {
			if block.String("type") == "text" {
				texts = append(texts, block.String("text"))
			}
		}
		if text := strings.Join(texts, "\n"); text != "" {
			return text
		}
	}
	return ""
}

func stringValueMain(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}
