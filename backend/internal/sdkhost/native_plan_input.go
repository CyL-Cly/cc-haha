package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

func (h *Host) processNativePlanInput(scope *cooperative.Scope, args string) (*sessioncontroller.ProcessedInput, error) {
	if h.options.Print.NativePlanCommand == nil || h.inputModelContext == nil {
		return nil, cap("actual /plan local JSX executor")
	}
	start := h.options.NowMillis()
	output, shouldQuery, err := h.options.Print.NativePlanCommand(h.inputModelContext, scope, args)
	if err != nil {
		return nil, err
	}
	records, err := h.BuildTerminalLocalJSXOnOwner(scope, "plan", args, &output, "", shouldQuery)
	if err != nil {
		return nil, err
	}
	return &sessioncontroller.ProcessedInput{TerminalLocalJSX: true, Messages: records, NoQuery: !shouldQuery, StartMillis: start, AttachmentsHandled: true}, nil
}
