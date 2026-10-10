package queryloop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func modelJSONString(raw json.RawMessage, field string) (json.RawMessage, error) {
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return nil, unsupported("non-string model tool "+field, err)
	}
	return protocol.QuoteJavaScriptUTF16(units), nil
}
func jsonCallContext(call JSONToolCallContext) (ToolCallContext, error) {
	id, err := modelExecutionString(call.ToolUseID, "id")
	return ToolCallContext{id, call.UserModified, call.Opaque, call.Invocation, call.Messages, call.QueryTracking}, err
}
func (batch *SerialToolBatch) progress(id json.RawMessage, active bool) error {
	if batch.capabilities.JSON != nil {
		return batch.capabilities.JSON.InProgress(append(json.RawMessage(nil), id...), active)
	}
	scalar, err := modelExecutionString(id, "id")
	if err != nil {
		return err
	}
	return batch.capabilities.InProgress(scalar, active)
}
func copyJSONCallContext(call JSONToolCallContext) JSONToolCallContext {
	call.ToolUseID = append(json.RawMessage(nil), call.ToolUseID...)
	return call
}
func (batch *SerialToolBatch) pre(ctx context.Context, tool RegisteredTool, input *protocol.JSONObject, call JSONToolCallContext) (*protocol.JSONObject, error) {
	if batch.capabilities.JSON != nil {
		return batch.capabilities.JSON.PreTool(ctx, tool, input, copyJSONCallContext(call))
	}
	scalar, err := jsonCallContext(call)
	if err != nil {
		return nil, err
	}
	return batch.capabilities.PreTool(ctx, tool, input, scalar)
}

func (batch *SerialToolBatch) preEffects(ctx context.Context, tool RegisteredTool, input *protocol.JSONObject, call *JSONToolCallContext, before, after *[]Message, emit func(Message) error) (*protocol.JSONObject, error) {
	if batch.capabilities.JSON != nil && batch.capabilities.JSON.PreToolEffects != nil {
		result, err := batch.capabilities.JSON.PreToolEffects(ctx, tool, input, copyJSONCallContext(*call), emit)
		*before = append(*before, result.Messages...)
		*after = append(*after, result.AfterMessages...)
		call.HookPermission = result.Permission
		call.PreToolStopped = result.Stop
		call.PreToolStopMessage = result.StopMessage
		return result.Input, err
	}
	return batch.pre(ctx, tool, input, *call)
}

func (batch *SerialToolBatch) emitPreStop(call JSONToolCallContext, emit func(Message) error) error {
	if call.PreToolStopMessage == nil {
		return unsupported("pre-tool stop without original feedback", ErrUnsupported)
	}
	if err := emit(*call.PreToolStopMessage); err != nil {
		return err
	}
	return batch.progress(call.ToolUseID, false)
}

func emitPreMessages(emit func(Message) error, before *[]Message) func(Message) error {
	return func(message Message) error {
		if message.Type() != "progress" {
			for len(*before) > 0 {
				m := (*before)[0]
				*before = (*before)[1:]
				if err := emit(m); err != nil {
					return err
				}
			}
		}
		return emit(message)
	}
}
func (batch *SerialToolBatch) permission(ctx context.Context, tool RegisteredTool, input *protocol.JSONObject, call JSONToolCallContext) (ToolPermission, error) {
	if batch.capabilities.JSON != nil {
		return batch.capabilities.JSON.ResolvePermission(ctx, tool, input, copyJSONCallContext(call))
	}
	scalar, err := jsonCallContext(call)
	if err != nil {
		return ToolPermission{}, err
	}
	return batch.capabilities.ResolvePermission(ctx, tool, input, scalar)
}
func (batch *SerialToolBatch) post(ctx context.Context, tool RegisteredTool, input *protocol.JSONObject, result ToolCallResult, call JSONToolCallContext) ([]Message, error) {
	if batch.capabilities.JSON != nil {
		return batch.capabilities.JSON.PostTool(ctx, tool, input, result, copyJSONCallContext(call))
	}
	scalar, err := jsonCallContext(call)
	if err != nil {
		return nil, err
	}
	return batch.capabilities.PostTool(ctx, tool, input, result, scalar)
}

func (batch *SerialToolBatch) postEffects(ctx context.Context, tool RegisteredTool, input *protocol.JSONObject, result *ToolCallResult, call JSONToolCallContext, emit func(Message) error) ([]Message, bool, error) {
	if batch.capabilities.JSON != nil && batch.capabilities.JSON.PostToolEffects != nil {
		value, err := batch.capabilities.JSON.PostToolEffects(ctx, tool, input, *result, copyJSONCallContext(call), emit)
		if value.UpdatedOutput != nil {
			if !tool.IsMCP {
				return nil, false, unsupported("non-MCP hook output replacement", ErrUnsupported)
			}
			result.Data = append(json.RawMessage(nil), value.UpdatedOutput...)
		}
		return value.Messages, value.UpdatedOutput != nil, err
	}
	messages, err := batch.post(ctx, tool, input, *result, call)
	return messages, false, err
}

func (batch *SerialToolBatch) nativeHookEffects() bool {
	return batch.capabilities.JSON != nil && batch.capabilities.JSON.PostToolEffects != nil
}
func callTool(ctx context.Context, tool RegisteredTool, input protocol.JSONObject, call JSONToolCallContext) (ToolCallResult, error) {
	if tool.CallJSON != nil {
		return tool.CallJSON(ctx, input, copyJSONCallContext(call))
	}
	scalar, err := jsonCallContext(call)
	if err != nil {
		return ToolCallResult{}, err
	}
	return tool.Call(ctx, input, scalar)
}
func mapTool(tool RegisteredTool, data, id json.RawMessage) (protocol.JSONObject, error) {
	if tool.MapResultJSON != nil {
		return tool.MapResultJSON(data, append(json.RawMessage(nil), id...))
	}
	scalar, err := modelExecutionString(id, "id")
	if err != nil {
		return protocol.JSONObject{}, err
	}
	return tool.MapResult(data, scalar)
}
func (batch *SerialToolBatch) requireIdentityCapabilities(id json.RawMessage, tool *RegisteredTool) error {
	if batch.capabilities.JSON != nil && (tool == nil || tool.CallJSON != nil && tool.MapResultJSON != nil) {
		return nil
	}
	_, err := modelExecutionString(id, "id")
	return err
}
func concatenate(parts ...json.RawMessage) (json.RawMessage, error) {
	return protocol.JoinJavaScriptValuesJSON(parts, "")
}
func (batch *SerialToolBatch) feedback(use Invocation, content protocol.JSONObject, result json.RawMessage, emit func(Message) error) error {
	raw, err := json.Marshal([]protocol.JSONObject{content})
	if err != nil {
		return err
	}
	return emit(CreateOrderedUserMessage(batch.factory, storage.Object{"content": raw, "toolUseResult": result, "sourceToolAssistantUUID": quoted(use.Assistant.UUID)}))
}
func (batch *SerialToolBatch) unknownFeedback(use Invocation, name, id json.RawMessage, emit func(Message) error) error {
	result, err := concatenate(quoted("Error: No such tool available: "), name)
	if err != nil {
		return err
	}
	text, err := concatenate(quoted("<tool_use_error>"), result, quoted("</tool_use_error>"))
	if err != nil {
		return err
	}
	return batch.feedback(use, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("tool_result")}, protocol.JSONField{Name: "content", Value: text}, protocol.JSONField{Name: "is_error", Value: json.RawMessage("true")}, protocol.JSONField{Name: "tool_use_id", Value: id}), result, emit)
}

// Source's marker precedes both registry lookup and cancellation. typeof len
// is number, including JSON numbers that become Infinity in JavaScript.
func unparsedMarker(raw json.RawMessage) (preview json.RawMessage, length json.RawMessage, previewLength int, ok bool) {
	var input storage.Object
	if json.Unmarshal(raw, &input) != nil || len(input) != 1 {
		return
	}
	markerRaw := input["__unparsedToolInput"]
	if len(markerRaw) == 0 {
		return
	}
	var marker protocol.JSONObject
	if json.Unmarshal(markerRaw, &marker) != nil || !marker.IsObject() {
		return
	}
	units, err := protocol.DecodeJavaScriptJSONString(marker.Get("raw"))
	if err != nil {
		return
	}
	length = bytes.TrimSpace(marker.Get("len"))
	if len(length) == 0 || !(length[0] == '-' || length[0] >= '0' && length[0] <= '9') || !json.Valid(length) {
		return nil, nil, 0, false
	}
	if len(units) > 200 {
		units = units[:200]
	}
	previewLength = len(units)
	preview = protocol.QuoteJavaScriptUTF16(units)
	ok = true
	return
}
func (batch *SerialToolBatch) markerFeedback(use Invocation, name, id, preview, length json.RawMessage, previewLength int, emit func(Message) error) error {
	lenText, err := protocol.JavaScriptStringJSON(length)
	if err != nil {
		return err
	}
	text, err := concatenate(quoted("<tool_use_error>InputValidationError: "), name, quoted(" was called with input that could not be parsed as JSON.\nYou sent (first "+fmt.Sprint(previewLength)+" of "), lenText, quoted(" bytes): "), preview, quoted("\nCommon causes: unescaped backslashes in file paths (use / or \\\\), unescaped control characters, or truncated output. Retry with valid JSON.</tool_use_error>"))
	if err != nil {
		return err
	}
	result, err := concatenate(quoted("InputValidationError: JSON parse failed ("), lenText, quoted(" bytes)"))
	if err != nil {
		return err
	}
	return batch.feedback(use, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("tool_result")}, protocol.JSONField{Name: "tool_use_id", Value: id}, protocol.JSONField{Name: "is_error", Value: json.RawMessage("true")}, protocol.JSONField{Name: "content", Value: text}), result, emit)
}

const cancelMessage = "The user doesn't want to take this action right now. STOP what you are doing and wait for the user to tell you how to proceed."

func (batch *SerialToolBatch) cancellationFeedback(use Invocation, id json.RawMessage, emit func(Message) error) error {
	if _, err := protocol.DecodeJavaScriptJSONString(batch.capabilities.CancellationContent); err != nil {
		return unsupported("unresolved memory correction cancellation text", errors.New("explicit resolved cancellation content required"))
	}
	return batch.feedback(use, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("tool_result")}, protocol.JSONField{Name: "content", Value: batch.capabilities.CancellationContent}, protocol.JSONField{Name: "is_error", Value: json.RawMessage("true")}, protocol.JSONField{Name: "tool_use_id", Value: id}), quoted(cancelMessage), emit)
}
