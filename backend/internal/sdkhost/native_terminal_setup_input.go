package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

func (h *Host) processNativeTerminalSetupInput(scope *cooperative.Scope, args string) (*sessioncontroller.ProcessedInput, error) {
	if h.options.Print.NativeTerminalSetupCommand == nil || h.inputModelContext == nil {
		return nil, cap("actual /terminal-setup local JSX executor")
	}
	start := h.options.NowMillis()
	output, err := h.options.Print.NativeTerminalSetupCommand(h.inputModelContext, scope, args)
	if err != nil {
		return nil, err
	}
	records, err := h.BuildTerminalLocalJSXOnOwner(scope, "terminal-setup", args, &output, "", false)
	if err != nil {
		return nil, err
	}
	return &sessioncontroller.ProcessedInput{TerminalLocalJSX: true, Messages: records, NoQuery: true, StartMillis: start, AttachmentsHandled: true}, nil
}
