package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

func (h *Host) CheckTerminalResumeOnOwner(scope *cooperative.Scope) error {
	if h == nil || scope == nil || !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess || !h.options.Print.NativeInteractive || h.askRunning || h.closing {
		return cooperative.ErrRestricted
	}
	return nil
}

func (h *Host) PrepareTerminalResumeOnOwner(scope *cooperative.Scope, log protocol.JSONObject) (*sessioncontroller.TerminalResume, error) {
	if err := h.CheckTerminalResumeOnOwner(scope); err != nil {
		return nil, err
	}
	return h.options.Controller.PrepareTerminalResumeOnOwner(scope, log)
}

func (h *Host) InstallTerminalResumeOnOwner(scope *cooperative.Scope, value *sessioncontroller.TerminalResume) error {
	if err := h.CheckTerminalResumeOnOwner(scope); err != nil {
		return err
	}
	return h.serial.InstallTerminalResumeOnOwner(scope, value)
}
