package queryloop

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type toolPartition struct {
	concurrent bool
	uses       []Invocation
}

// Execute retains Source's consecutive readonly partitions. Native operations
// run concurrently; completions and context changes remain on the one Owner.
func (batch *SerialToolBatch) Execute(ctx context.Context, round Round, uses []Invocation, emit func(Message) error) (any, error) {
	if !batch.general {
		return batch.executeSequential(ctx, round, uses, emit)
	}
	if emit == nil {
		return nil, errors.New("tool batch requires a consumer")
	}
	parts := []toolPartition{}
	for _, use := range uses {
		if use.Block == nil || use.Assistant == nil {
			return nil, errors.New("tool invocation requires its original assistant envelope")
		}
		name, err := modelJSONString(use.Block.Object.Get("name"), "name")
		if err != nil {
			return nil, err
		}
		id, err := modelJSONString(use.Block.Object.Get("id"), "id")
		if err != nil {
			return nil, err
		}
		tool := batch.findJSON(name)
		if err = batch.requireIdentityCapabilities(id, tool); err != nil {
			return nil, err
		}
		safe := false
		if tool != nil {
			if in, e := invocationInput(use); e == nil {
				if parsed, e := tool.ParseInput(in); e == nil {
					safe = concurrencySafe(*tool, parsed)
				}
			}
		}
		if len(parts) > 0 && parts[len(parts)-1].concurrent == safe {
			parts[len(parts)-1].uses = append(parts[len(parts)-1].uses, use)
		} else {
			parts = append(parts, toolPartition{safe, []Invocation{use}})
		}
	}
	current := round.ToolContext
	for _, part := range parts {
		round.ToolContext = current
		var err error
		if part.concurrent {
			current, err = batch.executeConcurrent(ctx, round, part.uses, emit)
		} else {
			current, err = batch.executeSequential(ctx, round, part.uses, emit)
		}
		if err != nil {
			return nil, err
		}
	}
	return current, nil
}

type pendingTool struct {
	before, after []Message
	index         int
	use           Invocation
	id            json.RawMessage
	tool          *RegisteredTool
	processed     *protocol.JSONObject
	call          JSONToolCallContext
	permission    ToolPermission
}
type completedTool struct {
	pending pendingTool
	result  ToolCallResult
	failure error
}

func (batch *SerialToolBatch) prepareConcurrent(ctx context.Context, round Round, use Invocation, index int, emit func(Message) error) (*pendingTool, protocol.JSONObject, error) {
	batchEmit := emit
	var before, after []Message
	emit = emitPreMessages(batchEmit, &before)
	id, err := modelJSONString(use.Block.Object.Get("id"), "id")
	if err != nil {
		return nil, protocol.JSONObject{}, err
	}
	tool := batch.findJSON(use.Block.Object.Get("name"))
	if tool == nil || tool.StartCallJSON == nil {
		return nil, protocol.JSONObject{}, unsupported("readonly tool actual asynchronous native call", ErrUnsupported)
	}
	if err = batch.progress(id, true); err != nil {
		return nil, protocol.JSONObject{}, err
	}
	fail := func(detail string) (*pendingTool, protocol.JSONObject, error) {
		if err := batch.failureFeedback(use, id, detail, emit); err != nil {
			return nil, protocol.JSONObject{}, err
		}
		return nil, protocol.JSONObject{}, batch.progress(id, false)
	}
	failCall := func(failure error) (*pendingTool, protocol.JSONObject, error) {
		if err := batch.callFailureFeedback(use, id, tool.Name, failure, emit); err != nil {
			return nil, protocol.JSONObject{}, err
		}
		return nil, protocol.JSONObject{}, batch.progress(id, false)
	}
	if ctx.Err() != nil {
		if err = batch.cancellationFeedback(use, id, emit); err != nil {
			return nil, protocol.JSONObject{}, err
		}
		return nil, protocol.JSONObject{}, batch.progress(id, false)
	}
	in, err := invocationInput(use)
	if err != nil {
		return nil, protocol.JSONObject{}, err
	}
	parsed, err := tool.ParseInput(in)
	if err != nil {
		if e := batch.schemaFailureFeedback(use, id, tool.Name, err, emit); e != nil {
			return nil, protocol.JSONObject{}, e
		}
		return nil, protocol.JSONObject{}, batch.progress(id, false)
	}
	if tool.ValidateInput != nil {
		if err = tool.ValidateInput(ctx, parsed, round.ToolContext); err != nil {
			return fail(err.Error())
		}
	}
	call := JSONToolCallContext{OnProgress: func(childID json.RawMessage, data protocol.JSONObject) error {
		return emit(Message{Record: batch.factory.CreateProgress(childID, id, data)})
	}, ToolUseID: id, Opaque: round.ToolContext, Invocation: use, Messages: round.History, QueryTracking: round.Tracking}
	if tool.NormalizeInputJSON != nil {
		parsed, err = tool.NormalizeInputJSON(ctx, parsed, call)
		if err == nil && !parsed.IsObject() {
			err = errors.New("normalized tool input must be an object")
		}
		if err != nil {
			return failCall(err)
		}
	}
	original, processed := &parsed, &parsed
	var backfilled *protocol.JSONObject
	if tool.Backfill != nil {
		copy := parsed.Clone()
		backfilled = &copy
		if err = tool.Backfill(backfilled); err != nil {
			return failCall(err)
		}
		processed = backfilled
	}
	processed, err = batch.preEffects(ctx, *tool, processed, &call, &before, &after, batchEmit)
	if err != nil {
		return failCall(err)
	}
	if processed == nil || !processed.IsObject() {
		return nil, protocol.JSONObject{}, unsupported("pre-tool input replacement", ErrUnsupported)
	}
	if call.PreToolStopped {
		return nil, protocol.JSONObject{}, batch.emitPreStop(call, emit)
	}
	permission, err := batch.permission(ctx, *tool, processed, call)
	if err != nil {
		if fatalToolError(err) {
			return nil, protocol.JSONObject{}, err
		}
		return failCall(err)
	}
	if permission.Behavior != "allow" || permission.Input == nil || !permission.Input.IsObject() {
		if permission.Behavior != "deny" && permission.Behavior != "ask" {
			return nil, protocol.JSONObject{}, unsupported("tool permission feedback", ErrUnsupported)
		}
		if err := batch.permissionFeedbackHistory(use, id, permission, round.History, emit); err != nil {
			return nil, protocol.JSONObject{}, err
		}
		return nil, protocol.JSONObject{}, batch.progress(id, false)
	}
	processed = permission.Input
	if permission.UpdatedInput != nil {
		processed = permission.UpdatedInput
	}
	if !processed.IsObject() {
		return nil, protocol.JSONObject{}, unsupported("permission input replacement", ErrUnsupported)
	}
	call.UserModified = permission.UserModified
	in, err = callInput(original, backfilled, processed)
	if err != nil {
		return failCall(err)
	}
	return &pendingTool{before: before, after: after, index: index, use: use, id: id, tool: tool, processed: processed, call: call, permission: permission}, in, nil
}

func (batch *SerialToolBatch) completeConcurrent(ctx context.Context, done completedTool, emit func(Message) error) (bool, error) {
	p := done.pending
	emit = emitPreMessages(emit, &p.before)
	fail := func(failure error) (bool, error) {
		return false, batch.innerFailureWithHooks(ctx, p.use, p.id, *p.tool, p.processed, failure, p.call, emit)
	}
	if done.failure != nil {
		return fail(done.failure)
	}
	structured, err := batch.structuredAttachment(done.result.StructuredOutput)
	if err != nil {
		return false, err
	}
	mapped, err := mapTool(*p.tool, done.result.Data, p.id)
	if err != nil {
		return fail(err)
	}
	mapped, err = batch.processToolResult(ctx, mapped, p.tool.Name, p.tool.PersistenceThreshold)
	if err != nil {
		return fail(err)
	}
	options, err := permissionUserContent(mapped, p.permission, p.call.Messages)
	if err != nil {
		return fail(err)
	}
	options["sourceToolAssistantUUID"] = quoted(p.use.Assistant.UUID)
	if !batch.capabilities.OmitSuccessfulToolUseResult {
		options["toolUseResult"] = done.result.Data
	}
	if done.result.MCPMeta != nil && !batch.capabilities.OmitMCPMeta {
		options["mcpMeta"] = done.result.MCPMeta
	}
	var message Message
	if !p.tool.IsMCP || !batch.nativeHookEffects() {
		message = CreateOrderedUserMessage(batch.factory, options)
	}
	post, changed, err := batch.postEffects(ctx, *p.tool, p.processed, &done.result, p.call, emit)
	if err != nil {
		return fail(err)
	}
	if changed {
		mapped, err = mapTool(*p.tool, done.result.Data, p.id)
		if err == nil {
			mapped, err = batch.processToolResult(ctx, mapped, p.tool.Name, p.tool.PersistenceThreshold)
		}
		if err == nil {
			options, err = permissionUserContent(mapped, p.permission, p.call.Messages)
		}
		if err != nil {
			return false, err
		}
		options["sourceToolAssistantUUID"] = quoted(p.use.Assistant.UUID)
		if !batch.capabilities.OmitSuccessfulToolUseResult {
			options["toolUseResult"] = done.result.Data
		}
		if done.result.MCPMeta != nil && !batch.capabilities.OmitMCPMeta {
			options["mcpMeta"] = done.result.MCPMeta
		}
	}
	if p.tool.IsMCP && batch.nativeHookEffects() {
		message = CreateOrderedUserMessage(batch.factory, options)
	}
	if structured != nil {
		if err := emit(*structured); err != nil {
			return false, err
		}
	}
	if err = emit(message); err != nil {
		return false, err
	}
	if batch.nativeHookEffects() && !p.tool.IsMCP {
		for _, value := range post {
			if err = emit(value); err != nil {
				return false, err
			}
		}
		post = nil
	}
	for _, message := range done.result.NewMessages {
		if err = emit(message); err != nil {
			return false, err
		}
	}
	for _, message := range p.after {
		if err = emit(message); err != nil {
			return false, err
		}
	}
	for _, message := range post {
		if err = emit(message); err != nil {
			return false, err
		}
	}
	return true, batch.progress(p.id, false)
}

func (batch *SerialToolBatch) executeConcurrent(ctx context.Context, round Round, uses []Invocation, emit func(Message) error) (any, error) {
	scope, owner := batch.capabilities.Scope, batch.capabilities.Owner
	if owner == nil || scope == nil || !scope.BelongsTo(owner) {
		return nil, unsupported("readonly batch original cooperative owner", ErrUnsupported)
	}
	limit := batch.capabilities.MaxConcurrency
	if limit == 0 {
		limit = 10
	}
	if limit < 1 {
		// Source generators.all starts no generators for a negative cap. Keep
		// this partition's context and let subsequent serial partitions run.
		return round.ToolContext, nil
	}
	workContext, cancel := context.WithCancel(ctx)
	defer cancel()
	queue := []completedTool{}
	var signal *ownerasync.OwnerFuture[struct{}]
	modifiers := make([]func(any) (any, error), len(uses))
	active, next := 0, 0
	for next < len(uses) || active > 0 {
		for next < len(uses) && active < limit {
			pending, input, err := batch.prepareConcurrent(workContext, round, uses[next], next, emit)
			next++
			if err != nil {
				return nil, err
			}
			if pending == nil {
				continue
			}
			future, err := pending.tool.StartCallJSON(workContext, input, pending.call)
			if err != nil {
				if _, err = batch.completeConcurrent(workContext, completedTool{pending: *pending, failure: err}, emit); err != nil {
					return nil, err
				}
				continue
			}
			if future == nil {
				return nil, unsupported("readonly native completion future", ErrUnsupported)
			}
			active++
			_, err = future.Then(scope, func(callback *cooperative.Scope, result ToolCallResult, failure error) error {
				queue = append(queue, completedTool{*pending, result, failure})
				if signal != nil {
					return signal.Resolve(callback, struct{}{})
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
		if active == 0 {
			continue
		}
		if len(queue) == 0 {
			signal, _ = ownerasync.NewOwnerFuture[struct{}](owner)
			if _, err := signal.Await(scope); err != nil {
				return nil, err
			}
			signal = nil
		}
		if len(queue) == 0 {
			return nil, unsupported("readonly completion without original native value", ErrUnsupported)
		}
		done := queue[0]
		queue = queue[1:]
		active--
		success, err := batch.completeConcurrent(workContext, done, emit)
		if err != nil {
			return nil, err
		}
		if success {
			modifiers[done.pending.index] = done.result.ModifyContext
		}
	}
	current := round.ToolContext
	for _, modify := range modifiers {
		if modify != nil {
			var err error
			current, err = modify(current)
			if err != nil {
				return nil, err
			}
		}
	}
	return current, nil
}
