package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func (h *Host) processNativeRewindInput(scope *cooperative.Scope, args string) (*sessioncontroller.ProcessedInput, error) {
	if h.options.Print.NativeRewindCommand == nil || h.inputModelContext == nil {
		return nil, cap("actual /rewind local command executor")
	}
	start := h.options.NowMillis()
	// Source allocates both local messages before loading the command, then
	// discards them when its actual call returns { type: 'skip' }.
	tagged := "<command-name>/rewind</command-name>\n            <command-message>rewind</command-message>\n            <command-args>" + args + "</command-args>"
	_ = h.options.Factory.CreateUser(storage.Object{"content": quote(tagged)})
	_ = h.options.Factory.CreateLocalCaveat()
	if err := scope.AwaitFulfilled(); err != nil {
		return nil, err
	}
	if err := h.options.Print.NativeRewindCommand(h.inputModelContext, scope); err != nil {
		return nil, err
	}
	return &sessioncontroller.ProcessedInput{TerminalRewindSkip: true, NoQuery: true, StartMillis: start, AttachmentsHandled: true}, nil
}
