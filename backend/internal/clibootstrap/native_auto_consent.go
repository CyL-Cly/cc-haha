package clibootstrap

import (
	"context"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type NativeAutoConsent struct {
	ID, Previous string
	Startup      bool
}

func (m *MainPrint) TerminalAutoConsent(ctx context.Context) (*NativeAutoConsent, error) {
	var result *NativeAutoConsent
	// REPL mounts this read after the actual pane inbox effect may already
	// have started its model continuation. Observing pending consent is not a
	// second ASK, and must remain readable while that continuation is parked.
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if m.nativeAutoConsent != nil {
			copy := *m.nativeAutoConsent
			result = &copy
		}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}

func (m *MainPrint) ResolveTerminalAutoConsent(ctx context.Context, id string, accepted, makeDefault bool) (mode string, startupDeclined bool, err error) {
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		pending := m.nativeAutoConsent
		if pending == nil || pending.ID != id {
			return errors.New("Auto mode consent is no longer pending")
		}
		mode = pending.Previous
		if accepted {
			patch := protocol.NewJSONObject(protocol.JSONField{Name: "skipAutoPermissionPrompt", Value: rawMain(true)})
			if makeDefault {
				patch.Set("permissions", rawMain(map[string]string{"defaultMode": "auto"}))
			}
			result, err := m.settings.UpdatePermissionEditingSource("userSettings", patch, clisettings.PermissionEditingBindings{})
			if err != nil {
				return err
			}
			if result.Error != nil {
				return result.Error
			}
			rules, err := m.nativePermissionRules()
			if err != nil {
				return err
			}
			if err = m.Host.ApplyNativeSettingsChange(scope, rules); err != nil {
				return err
			}
			mode = "auto"
		}
		if err := m.Host.SetNativePermissionMode(scope, mode); err != nil {
			return err
		}
		if !accepted {
			if err := m.Controller.DeclineNativeAutoMode(scope); err != nil {
				return err
			}
			startupDeclined = pending.Startup
		}
		m.nativeAutoConsent = nil
		if m.options.OnPermissionModeChanged != nil {
			return m.options.OnPermissionModeChanged(ctx, mode)
		}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return
}
