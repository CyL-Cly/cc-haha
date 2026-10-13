package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
)

// State belongs exclusively to the parent Owner. The physical child runs the
// existing MainPrint/ASK/Writer on its own Owner, with the same shared settings,
// accounting relay, permissions and parent task producer as a background child.
type nativeForegroundAgent struct {
	releaseSlot                   func()
	backgrounded                  atomic.Bool
	parent, child                 *MainPrint
	id, path, directory, streamID string
	request                       builtintools.AgentRequest
	tree                          *nativeAgentWorktree
	start                         float64
	progress                      *foregroundAgentProgress
	messages                      []queryloop.Message
	result                        *ownerasync.OwnerFuture[builtintools.AgentRunResult]
	oldDone, taskDone             chan struct{}
	promoted, registered          bool
	unregisterCleanup             runtimecleanup.Unregister
	stopAuto                      func()
	oldContext                    context.Context
	cancelOld, cancelTask         context.CancelCauseFunc
	taskContext                   context.Context
}

func (m *MainPrint) startNativeForegroundAgent(ctx context.Context, scope *cooperative.Scope, request builtintools.AgentRequest) (*ownerasync.OwnerFuture[builtintools.AgentRunResult], error) {
	if !scope.BelongsTo(m.Owner) || m.nativeModelSelector == nil || m.prompt == nil {
		return nil, cooperative.ErrRestricted
	}
	releaseSlot, e := m.reserveOrdinarySubagent()
	if e != nil {
		return nil, e
	}
	transferred := false
	defer func() {
		if !transferred {
			releaseSlot()
		}
	}()
	id, e := nativeAgentID()
	if e != nil {
		return nil, e
	}
	tree, e := m.prepareAgentWorktree(ctx, scope, request, id)
	if e != nil {
		return nil, e
	}
	fail := func(e error) (*ownerasync.OwnerFuture[builtintools.AgentRunResult], error) {
		_, _ = m.finishAgentWorktree(scope, tree)
		return nil, e
	}
	parentModel := request.ParentModel
	if parentModel == "" {
		parentModel, e = m.Host.InferenceModel(scope)
		if e != nil {
			return fail(e)
		}
	}
	var modelArg, definitionArg json.RawMessage
	if request.Model != "" {
		modelArg = quoteMain(request.Model)
	}
	if request.DefinitionModel != "" {
		definitionArg = quoteMain(request.DefinitionModel)
	}
	mode := request.ParentPermissionMode
	if mode == "" {
		mode = request.PermissionContext.String("mode")
	}
	selected, e := m.nativeModelSelector.AgentModel(modelselection.AgentInput{AgentModel: definitionArg, ParentModel: quoteMain(parentModel), ToolSpecifiedModel: modelArg, PermissionMode: mode})
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
	environment, e := m.prompt.AgentEnvironmentAt(ctx, model, nil, tree.CWD, func(context.Context) (bool, error) {
		return usercontext.GitRoot(mainSettingsFiles{n: m.options.Native}, tree.CWD) != "", nil
	})
	if e != nil {
		return fail(e)
	}
	sections = append(sections, environment)
	directory, e := m.nativeTaskOutputDirectory(scope)
	if e != nil {
		return fail(e)
	}
	o, e := m.nativeIndependentAgentOptions(scope, request, id, tree.CWD, model, directory, sections, nil)
	if e != nil {
		return fail(e)
	}
	result, e := ownerasync.NewOwnerFuture[builtintools.AgentRunResult](m.Owner)
	if e != nil {
		return fail(e)
	}
	streamID, e := nativeAgentID()
	if e != nil {
		return fail(e)
	}
	oldCtx, cancelOld := context.WithCancelCause(context.Background())
	taskCtx, cancelTask := context.WithCancelCause(context.Background())
	state := &nativeForegroundAgent{releaseSlot: releaseSlot, parent: m, id: id, directory: directory, streamID: streamID, request: request, tree: tree, start: m.options.Native.NowMillis(), progress: m.nativeAgentProgress(request, tree.CWD), result: result, oldDone: make(chan struct{}), taskDone: make(chan struct{}), oldContext: oldCtx, cancelOld: cancelOld, taskContext: taskCtx, cancelTask: cancelTask}
	o.DetachedAgent.isBackgrounded = state.backgrounded.Load
	o.DetachedAgent.iteratorReady = func(childScope *cooperative.Scope) error {
		return bridgeAgentVoid(state.child, m, childScope, func(parentScope *cooperative.Scope) error {
			if !state.registered && !skillTruthy(m.options.Native.Environment["CLAUDE_CODE_DISABLE_BACKGROUND_TASKS"]) {
				return state.register(parentScope)
			}
			return nil
		})
	}
	o.OnQueryYield = func(childScope *cooperative.Scope, yield *queryloop.Yield) error {
		var data json.RawMessage
		var e error
		if yield.Stream != nil {
			data, e = yield.Stream.EventJSON()
		} else if yield.Message != nil && (yield.Message.Type() == "assistant" || yield.Message.Type() == "user") {
			data, e = yield.Message.MarshalJSON()
		}
		if e != nil {
			return e
		}
		return bridgeAgentVoid(state.child, m, childScope, func(parentScope *cooperative.Scope) error {
			if state.promoted {
				if data != nil {
					return state.emit("message", data, nil)
				}
				return nil
			}
			if !state.registered && !skillTruthy(m.options.Native.Environment["CLAUDE_CODE_DISABLE_BACKGROUND_TASKS"]) {
				if e := state.register(parentScope); e != nil {
					return e
				}
			}
			if data == nil {
				return nil
			}
			if yield.Message != nil {
				var original protocol.JSONObject
				if e := original.UnmarshalJSON(data); e != nil {
					return e
				}
				state.messages = append(state.messages, queryloop.Message{Record: original.Clone()})
			}
			last := state.progress.observe(data)
			if state.progress.failure != nil {
				return state.progress.failure
			}
			if state.registered && last != "" {
				if e := m.emitForegroundAgentProgress(parentScope, id, request, state.start, state.progress, last); e != nil {
					return e
				}
			}
			return state.emit("message", data, nil)
		})
	}
	type startupChild struct {
		process *MainPrint
		path    string
	}
	binding, e := m.Controller.BorrowNativeCopyBinding(m.Owner)
	if e != nil {
		return fail(e)
	}
	ref, e := binding.Acquire(scope)
	if e != nil {
		return fail(e)
	}
	startup, e := nativeSkillWork(m, scope, func() (startupChild, error) {
		child, e := NewMainPrint(o)
		if e != nil {
			return startupChild{}, e
		}
		if e = child.StartSession(context.Background()); e != nil {
			child.Close()
			return startupChild{}, e
		}
		var path string
		e = child.Owner.Run(context.Background(), func(childScope *cooperative.Scope) error {
			if e := child.hook.EmptyEvent("SubagentStart", id); e != nil {
				return e
			}
			var e error
			path, e = child.Controller.Writer().ComputedTranscriptPath(childScope)
			return e
		})
		if e != nil {
			_ = child.ShutdownSession(context.Background())
			return startupChild{}, e
		}
		return startupChild{child, path}, nil
	})
	if e != nil {
		ref.DeliveryFinished()
		cancelOld(e)
		cancelTask(e)
		return fail(e)
	}
	initialized, e := startup.Await(scope)
	if e != nil {
		cancelOld(e)
		cancelTask(e)
		// Cancellation of the observer does not erase its admitted native child.
		// Reclaim the actual constructor result and join that process before release.
		go func() {
			defer ref.DeliveryFinished()
			actual, failure := startup.Wait(context.Background())
			if failure == nil && actual.process != nil {
				_ = actual.process.ShutdownSession(context.Background())
			}
		}()
		return fail(e)
	}
	state.child, state.path = initialized.process, initialized.path
	m.writeAgentWorktreeMetadata(state.path, nativeAgentMetadata(request, tree, m.options.AgentID, false))
	// Cancellation follows the original foreground caller until promotion. The
	// actual child transport is preserved when iterator.return queues behind next.
	go func() {
		select {
		case <-ctx.Done():
			receipt, e := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(*cooperative.Scope) error {
				if !state.promoted {
					state.cancelOld(context.Cause(ctx))
				}
				return nil
			})
			if e == nil {
				_ = receipt.Wait()
			}
		case <-state.oldDone:
		}
	}()
	state.unregisterCleanup, e = m.Cleanup.Register(scope, runtimecleanup.NewCallback(func(*cooperative.Scope) (*runtimecleanup.Future, error) {
		state.cancelOld(context.Canceled)
		state.cancelTask(context.Canceled)
		return nil, nil
	}))
	if e != nil {
		cancelOld(e)
		cancelTask(e)
		go func() { defer ref.DeliveryFinished(); _ = state.child.ShutdownSession(context.Background()) }()
		return fail(e)
	}
	transferred = true
	go state.run(ref)
	return result, nil
}

func (state *nativeForegroundAgent) emit(kind string, data json.RawMessage, failure error) error {
	event := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("system")}, protocol.JSONField{Name: "subtype", Value: quoteMain("agent_run_message")}, protocol.JSONField{Name: "run_agent_id", Value: quoteMain(state.id)}, protocol.JSONField{Name: "stream_id", Value: quoteMain(state.streamID)}, protocol.JSONField{Name: "target_agent_id", Value: quoteMain(state.id)}, protocol.JSONField{Name: "event_kind", Value: quoteMain(kind)}, protocol.JSONField{Name: "uuid", Value: quoteMain(uuidMain())}, protocol.JSONField{Name: "session_id", Value: quoteMain(state.parent.Controller.SessionID())})
	if data != nil {
		event.Set("message", data)
	}
	if failure != nil {
		event.Set("error", quoteMain(failure.Error()))
	}
	bytes, e := event.MarshalJSON()
	if e != nil {
		return e
	}
	return state.parent.enqueue(bytes)
}
func (state *nativeForegroundAgent) register(scope *cooperative.Scope) error {
	m := state.parent
	cancel := func(e error) {
		state.cancelTask(e)
		state.cancelOld(e)
	}
	if e := m.beginForegroundAgentTask(scope, state.id, state.path, state.request, cancel, state.taskDone); e != nil {
		return e
	}
	stop, e := m.bindNativeForegroundPromotionAction(scope, state.id, state.promote)
	if e != nil {
		return e
	}
	state.stopAuto = stop
	state.registered = true
	return nil
}
func (state *nativeForegroundAgent) promote(scope *cooperative.Scope) error {
	if state.promoted {
		return nil
	}
	state.promoted = true
	if state.stopAuto != nil {
		state.stopAuto()
	}
	// The same admitted iterator, provider stream and sidechain continue. Only
	// its interaction boundary changes; no prompt/history is replayed.
	state.backgrounded.Store(true)
	return state.result.Resolve(scope, builtintools.AgentRunResult{AgentID: state.id, Async: true, OutputFile: filepath.Join(state.directory, state.id+".output")})
}
func (state *nativeForegroundAgent) run(ref *ownerasync.NativeReference) {
	child, m := state.child, state.parent
	var history []queryloop.Message
	failure := child.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		input := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "session_id", Value: quoteMain(child.Controller.SessionID())}, protocol.JSONField{Name: "message", Value: rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quoteMain("user")}, protocol.JSONField{Name: "content", Value: quoteMain(state.request.Prompt)}))})
		if e := child.Host.RunOne(scope, state.oldContext, input); e != nil {
			return e
		}
		if child.Host.ExitCode() != 0 {
			return fmt.Errorf("Agent %s query failed", state.id)
		}
		var e error
		history, e = child.Host.OriginalHistoryOnOwner(scope)
		return e
	})
	_ = child.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		return child.killAgentOwnedShells(scope, state.id)
	})
	cleanup := child.ShutdownSession(context.Background())
	if failure == nil {
		failure = cleanup
	}
	delivery, e := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error {
		defer ref.DeliveryFinished()
		defer close(state.oldDone)
		defer close(state.taskDone)
		defer func() {
			// A normal foreground result does not abort Source's shared hook
			// controller. Keep only admitted SDK permission hooks alive through
			// their finally; failure, promotion and explicit stop retain cancellation.
			retained := false
			if failure == nil && !state.promoted && m.Host != nil {
				retained, _ = m.Host.RetainNativePermissionHookSignal(scope, func() { state.cancelOld(nil) })
			}
			if !retained {
				state.cancelOld(nil)
			}
		}()
		if state.unregisterCleanup != nil {
			_, _ = state.unregisterCleanup(scope)
		}
		if state.stopAuto != nil {
			state.stopAuto()
		}
		defer state.releaseSlot()
		defer state.cancelTask(nil)
		if state.promoted {
			run := builtintools.AgentRunResult{AgentID: state.id, Messages: history, DurationMillis: m.options.Native.NowMillis() - state.start}
			if e := m.completeNativeBackgroundAgent(scope, state.id, state.request, run, failure, state.progress, state.start, nil); e != nil {
				return e
			}
			retained, e := m.finishAgentWorktree(scope, state.tree)
			if e != nil {
				return e
			}
			if retained == nil && state.tree.service != nil {
				meta := nativeAgentMetadata(state.request, state.tree, m.options.AgentID, false)
				meta.Delete("worktreePath")
				m.writeAgentWorktreeMetadata(state.path, meta)
			}
			kind := "complete"
			if failure != nil {
				kind = "error"
				if errors.Is(failure, context.Canceled) {
					kind = "cancelled"
				}
			}
			return state.emit(kind, nil, failure)
		}
		defer state.cancelTask(nil)
		if state.registered {
			_ = m.endForegroundAgentTask(scope, state.id, state.request, state.start, failure, state.progress)
		}
		kept, e := m.finishAgentWorktree(scope, state.tree)
		if e != nil {
			failure = e
		}
		result := builtintools.AgentRunResult{AgentID: state.id, Messages: history, DurationMillis: m.options.Native.NowMillis() - state.start, WorktreeIsolationSkipped: state.tree.Skipped}
		if kept != nil {
			result.WorktreePath, result.WorktreeBranch = kept.Path, kept.Branch
		} else if state.tree.service != nil {
			meta := nativeAgentMetadata(state.request, state.tree, m.options.AgentID, false)
			meta.Delete("worktreePath")
			m.writeAgentWorktreeMetadata(state.path, meta)
		}
		if failure != nil {
			kind := "error"
			if errors.Is(failure, context.Canceled) || state.oldContext.Err() != nil {
				kind = "cancelled"
			}
			_ = state.emit(kind, nil, failure)
			return state.result.Reject(scope, failure)
		}
		if e = state.emit("complete", nil, nil); e != nil {
			return state.result.Reject(scope, e)
		}
		return state.result.Resolve(scope, result)
	})
	if e == nil {
		e = delivery.Wait()
	}
	if e != nil {
		ref.DeliveryFinished()
		state.releaseSlot()
		_ = state.result.RejectAdministrative(e)
		state.cancelOld(e)
		state.cancelTask(e)
	}
}
