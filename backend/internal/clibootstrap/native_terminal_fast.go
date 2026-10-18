package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainPrint) NativeFastCommandEnabled() bool {
	env := m.options.Native.Environment
	return !pickerTrue(env["CLAUDE_CODE_DISABLE_FAST_MODE"]) && m.nativeModelSelector.FirstPartyCommandAvailable()
}

func (m *MainPrint) terminalFastUnavailable(scope *cooperative.Scope) (string, error) {
	if err := m.initializeNativeModelCommandState(scope); err != nil {
		return "", err
	}
	env, err := m.pickerEnvironment()
	if err != nil {
		return "", err
	}
	if pickerTrue(env["CLAUDE_CODE_DISABLE_FAST_MODE"]) {
		return "Fast mode is not available", nil
	}
	if !nativeCommandFirstParty(env) {
		return "Fast mode is not available on Bedrock, Vertex, or Foundry", nil
	}
	runtime := m.modelCommandRuntime
	if runtime.orgEnabled {
		return "", nil
	}
	if (runtime.orgReason == "network_error" || runtime.orgReason == "unknown") && pickerTrue(env["CLAUDE_CODE_SKIP_FAST_MODE_NETWORK_ERRORS"]) {
		return "", nil
	}
	switch runtime.orgReason {
	case "free":
		return "Fast mode unavailable during evaluation. Please purchase credits.", nil
	case "extra_usage_disabled":
		return "Fast mode requires extra usage billing · /extra-usage to enable", nil
	case "network_error":
		return "Fast mode unavailable due to network connectivity issues", nil
	case "preference":
		return "Fast mode has been disabled by your organization", nil
	default:
		return "Fast mode is currently unavailable", nil
	}
}
func (m *MainPrint) TerminalFastMode(ctx context.Context, enabled *bool) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if m.closed {
			return cooperative.ErrClosed
		}
		reason, err := m.terminalFastUnavailable(scope)
		if err != nil {
			return err
		}
		state, err := m.Controller.TypedModelStateOnOwner(scope)
		if err != nil {
			return err
		}
		if enabled != nil {
			switched := false
			if *enabled && reason != "" {
				return errors.New(reason)
			}
			patch := clisettings.Patch{Object: protocol.NewJSONObject()}
			if *enabled {
				patch.Object.Set("fastMode", []byte("true"))
			} else {
				patch.UndefinedPaths = [][]json.RawMessage{{quoteMain("fastMode")}}
			}
			if err = m.writePickerSetting(patch); err != nil {
				return err
			}
			rules, err := m.nativePermissionRules()
			if err != nil {
				return err
			}
			if err = m.Host.ApplyNativeSettingsChange(scope, rules); err != nil {
				return err
			}
			m.modelCommandRuntime.fast.ClearFastModeCooldown()
			if *enabled && !nativeCommandFastSupported(m.nativeModelSelector, state.Base) {
				model := "opus"
				env, err := m.pickerEnvironment()
				if err != nil {
					return err
				}
				settings, err := m.Startup.GetInitialSettings()
				if err != nil {
					return err
				}
				merged := protocol.NewJSONObject()
				if settings != nil {
					merged = settings.Clone()
				}
				selector, err := m.nativeModelSelection(ctx, scope, modelselection.Options{Environment: env, Settings: merged, Override: quoteMain(model)})
				if err != nil {
					return err
				}
				resolved, err := nativePickerResolve(selector, &model)
				if err != nil {
					return err
				}
				if err = m.Host.SetInferenceModel(scope, resolved, m.modelPicker.effort, true); err != nil {
					return err
				}
				state.Base = &model
				switched = true
				m.modelPicker.rawModel, m.nativeModelSelector, m.options.Arguments.Model = &model, selector, resolved
			}
			if switched {
				err = m.Controller.ApplyTypedModelState(scope, state.Base, *enabled)
			} else {
				err = m.Controller.SetTypedFastMode(scope, *enabled)
			}
			if err != nil {
				return err
			}
			state.FastMode = *enabled
		}
		result = protocol.NewJSONObject(protocol.JSONField{Name: "enabled", Value: rawMain(state.FastMode)}, protocol.JSONField{Name: "unavailableReason", Value: quoteMain(reason)})
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}
