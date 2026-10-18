package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

// TerminalShortcutHelp samples the actual PromptInputHelpMenu mount inputs.
// The org availability is the already initialized process fast-mode state;
// opening the footer never starts an account request.
func (m *MainPrint) TerminalShortcutHelp(ctx context.Context) (tui.TerminalShortcutHelp, error) {
	var result tui.TerminalShortcutHelp
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if m.closed || m.options.Native == nil || !m.options.Native.NativeProcess || !m.options.Interactive || m.agentChild || m.modelCommandRuntime == nil {
			return cooperative.ErrRestricted
		}
		global, err := m.Global.Get()
		if err != nil {
			return err
		}
		result.Platform = m.options.Platform
		if result.Platform == "darwin" {
			result.Platform = "macos"
		} else if result.Platform == "win32" {
			result.Platform = "windows"
		}
		result.Newline = "backslash (\\) + return (⏎) for newline"
		if m.nativeTerminalSetupTerminal == "Apple_Terminal" && m.options.Platform == "darwin" || global != nil && string(global.Get("shiftEnterKeyBindingInstalled")) == "true" {
			result.Newline = "shift + ⏎ for newline"
		} else if global != nil && string(global.Get("hasUsedBackslashReturn")) == "true" {
			result.Newline = "\\⏎ for newline"
		}
		reason, err := m.terminalFastUnavailable(scope)
		if err != nil {
			return err
		}
		result.FastModeAvailable = reason == ""
		result.KeybindingsEnabled = m.NativeKeybindingsEnabled()
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}
