package sdkhost

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

type TerminalBashProcessor func(*cooperative.Scope, context.Context, json.RawMessage) (*sessioncontroller.ProcessedInput, error)
type terminalBashInput struct {
	context context.Context
	process TerminalBashProcessor
	content json.RawMessage
}

// RunTerminalBash retains one original SerialASK input/finally/checkpoint path.
// Only the trusted terminal caller can supply a producer; parsed SDK fields and
// queued input cannot turn ordinary user text into a local shell invocation.
func (h *Host) RunTerminalBash(scope *cooperative.Scope, ctx context.Context, input protocol.JSONObject, process TerminalBashProcessor) error {
	if !scope.BelongsTo(h.options.Owner) || ctx == nil || process == nil || h.options.Print == nil || !h.options.Print.NativeProcess || h.terminalBash != nil || h.askRunning {
		return cooperative.ErrRestricted
	}
	message, _ := input.Object("message")
	h.terminalBash = &terminalBashInput{context: ctx, process: process, content: append(json.RawMessage(nil), message.Get("content")...)}
	defer func() { h.terminalBash = nil }()
	endInputContext := h.beginInputContext(ctx)
	initial, err := h.serial.BeginTerminalBash(scope, input)
	endInputContext()
	if err != nil {
		return err
	}
	if initial == nil {
		return cap("empty original terminal Bash admission")
	}
	return h.runAccepted(scope, ctx, initial)
}
func (h *Host) processTerminalBash(scope *cooperative.Scope, content json.RawMessage) (*sessioncontroller.ProcessedInput, error) {
	if h.terminalBash == nil || !scope.BelongsTo(h.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	if _, err := protocol.DecodeJavaScriptJSONString(content); err != nil {
		return nil, cap("terminal Bash preceding content producer")
	}
	return h.terminalBash.process(scope, h.terminalBash.context, content)
}
