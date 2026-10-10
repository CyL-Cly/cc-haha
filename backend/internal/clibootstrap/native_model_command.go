package clibootstrap

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type NativeModelCommandResult struct {
	Notice string
	System bool
}

// NativeModelCommand is the typed local JSX consumer. It owns validation and
// AppState/model persistence; SDK set_model's transcript breadcrumbs stay with
// its separate control handler.
func (m *MainPrint) NativeModelCommand(ctx context.Context, argument string) (NativeModelCommandResult, error) {
	var result NativeModelCommandResult
	err := m.Owner.Run(ctx, func(scope *cooperative.Scope) (failure error) {
		defer func() {
			if failure != nil || result.Notice == "" {
				return
			}
			rows, err := m.Controller.AppendTypedModelResult(scope, argument, result.Notice, result.System)
			if err != nil {
				failure = err
				return
			}
			if m.options.OnLocalInputMessages != nil {
				failure = m.options.OnLocalInputMessages(rows)
			}
		}()
		if m.closed || !m.initialized || m.Host == nil {
			return cooperative.ErrClosed
		}
		args := strings.TrimFunc(argument, modelCommandWhitespace)
		for _, info := range []string{"list", "show", "display", "current", "view", "get", "check", "describe", "print", "version", "about", "status", "?"} {
			if args == info {
				if err := m.initializeNativeModelCommandState(scope); err != nil {
					return err
				}
				state, err := m.Controller.TypedModelStateOnOwner(scope)
				if err != nil {
					return err
				}
				label, err := m.pickerLabel(m.nativeModelSelector, state.Base)
				if err != nil {
					return err
				}
				result.Notice = "Current model: " + label
				if state.ForSession != nil {
					sessionLabel, err := m.pickerLabel(m.nativeModelSelector, state.ForSession)
					if err != nil {
						return err
					}
					result.Notice = "Current model: " + sessionLabel + " (session override from plan mode)\nBase model: " + label
				}
				effort, _, err := m.Host.InferenceEffort(scope)
				if err != nil {
					return err
				}
				if effort != nil {
					text, err := protocol.JoinJavaScriptValues([]json.RawMessage{effort}, "")
					if err != nil {
						return err
					}
					result.Notice += " (effort: " + text + ")"
				}
				return nil
			}
		}
		for _, help := range []string{"help", "-h", "--help"} {
			if args == help {
				result = NativeModelCommandResult{Notice: "Run /model to open the model selection menu, or /model [modelName] to set the model.", System: true}
				return nil
			}
		}
		if args == "" {
			return mainCap("typed model command requires arguments")
		}
		if err := m.initializeNativeModelCommandState(scope); err != nil {
			return err
		}
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
		var model *string
		if args != "default" {
			model = pickerPointer(args)
		}
		override := json.RawMessage("null")
		if model != nil {
			override = quoteMain(*model)
		}
		selector, err := m.nativeModelSelection(ctx, scope, modelselection.Options{Environment: env, Settings: merged, Override: override})
		if err != nil {
			return err
		}
		if model != nil {
			allowed, err := selector.Allowed(quoteMain(*model))
			if err != nil {
				return err
			}
			if !allowed {
				result = NativeModelCommandResult{Notice: "Model '" + *model + "' is not available. Your organization restricts model selection.", System: true}
				return nil
			}
			// The access and real nonstream validation consumers are supplied by
			// the same native command producer, before any state/persistence.
			if notice, err := m.validateNativeModelCommand(ctx, scope, *model, selector, env, merged); err != nil {
				return err
			} else if notice != "" {
				result = NativeModelCommandResult{Notice: notice, System: true}
				return nil
			}
		}
		resolved, err := nativePickerResolve(selector, model)
		if err != nil {
			return err
		}
		label, err := m.pickerLabel(selector, model)
		if err != nil {
			return err
		}
		if !pickerSame(m.modelPicker.rawModel, model) {
			patch := clisettings.Patch{Object: protocol.NewJSONObject(protocol.JSONField{Name: "model", Value: override})}
			if model == nil {
				patch.Object.Set("model", nil)
				patch.UndefinedPaths = [][]json.RawMessage{{quoteMain("model")}}
			}
			if err = m.writePickerSetting(patch); err != nil {
				return err
			}
		}
		if err = m.Host.AcceptInternalModelSettings(scope); err != nil {
			return err
		}
		if err = m.Host.SetInferenceModel(scope, resolved, nil, false); err != nil {
			return err
		}
		m.modelPicker.rawModel = pickerClone(model)
		m.nativeModelSelector = selector
		m.options.Arguments.Model = resolved
		result.Notice = "Set model to " + label
		suffix, err := m.applyNativeSelectedModelState(ctx, scope, model, selector, env, false)
		if err != nil {
			return err
		}
		result.Notice += suffix

		return nil
	})
	if err != nil {
		return result, err
	}
	return result, nil
}

func modelCommandWhitespace(r rune) bool {
	return r >= '\t' && r <= '\r' || r == ' ' || r == '\u00a0' || r == '\u1680' || r >= '\u2000' && r <= '\u200a' || r == '\u2028' || r == '\u2029' || r == '\u202f' || r == '\u205f' || r == '\u3000' || r == '\ufeff'
}
