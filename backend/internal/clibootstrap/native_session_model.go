package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// SetSessionModel applies the original SDK set_model control to an already
// running main session. Global settings and persistence are separate callers.
func (m *MainPrint) SetSessionModel(ctx context.Context, requested string) error {
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		return m.applyNativeSessionModelOnOwner(ctx, scope, requested)
	})
	if err != nil {
		return err
	}
	return receipt.Wait()
}

// SessionModel is an owner-checked observation of the next ASK's actual model.
func (m *MainPrint) SessionModel(ctx context.Context) (string, error) {
	var model string
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		var err error
		model, err = m.Host.InferenceModel(scope)
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	return model, err
}

func (m *MainPrint) applyNativeSessionModelOnOwner(ctx context.Context, scope *cooperative.Scope, requested string) error {
	if m.Host == nil || !m.initialized || m.closed || m.options.Native == nil || m.options.AgentID != "" && m.teamWorker == nil {
		return cooperative.ErrRestricted
	}
	if requested == "" {
		requested = "default"
	}
	settings, err := m.Startup.GetInitialSettings()
	if err != nil {
		return err
	}
	merged := protocol.NewJSONObject()
	if settings != nil {
		merged = settings.Clone()
	}
	selector, err := m.nativeModelSelection(ctx, scope, modelselection.Options{Environment: m.nativeOpenAIEnvironment(), Settings: merged})
	if err != nil {
		return err
	}
	selected := requested
	if requested == "default" {
		parsed, err := selector.Parse(selector.DefaultSetting())
		if err != nil {
			return err
		}
		selected, err = decodeNativeModel(parsed)
		if err != nil {
			return err
		}
	}
	resolved, selector, err := m.resolveNativeModel(ctx, scope, settings, selected)
	if err != nil {
		return err
	}
	display, err := nativePickerDisplay(selector, &selected)
	if err != nil {
		return err
	}
	if err = m.Host.ApplyNativeSessionModel(scope, requested, resolved, display); err != nil {
		return err
	}
	m.nativeModelSelector = selector
	m.options.Arguments.Model = selected
	return nil
}
