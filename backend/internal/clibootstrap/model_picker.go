package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

type nativeModelPickerState struct {
	rawModel    *string
	initial     *string
	effort      json.RawMessage
	next        uint64
	pending     *tui.ModelPicker
	validModels map[string]bool
}

func pickerPointer(value string) *string { return &value }
func pickerClone(value *string) *string {
	if value == nil {
		return nil
	}
	return pickerPointer(*value)
}
func pickerSame(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func pickerTrue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func (m *MainPrint) pickerEnvironment() (map[string]string, error) {
	env := map[string]string{}
	for _, field := range m.environment.Snapshot().OwnProperties() {
		name, err := decodeNativeModel(field.NameJSON)
		if err != nil {
			return nil, err
		}
		value, err := decodeNativeModel(field.Value)
		if err != nil {
			return nil, mainCap("model picker environment scalar " + name)
		}
		env[name] = value
	}
	return env, nil
}

func (m *MainPrint) initializePicker(scope *cooperative.Scope) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	if m.options.Native == nil || !m.options.Native.NativeProcess || m.nativeModelSelector == nil {
		return mainCap("native interactive model picker host")
	}
	if m.modelPicker != nil {
		return nil
	}
	raw, err := m.nativeModelSelector.UserSpecified()
	if err != nil {
		return err
	}
	var initial *string
	if raw != nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		value, err := decodeNativeModel(raw)
		if err != nil {
			return err
		}
		initial = pickerPointer(value)
	}
	effort, err := m.initialNativeEffort()
	if err != nil {
		return err
	}
	m.modelPicker = &nativeModelPickerState{rawModel: pickerClone(initial), initial: pickerClone(initial), effort: effort}
	return nil
}

func pickerEffortLevel(raw json.RawMessage) *string {
	if raw == nil {
		return nil
	}
	var level string
	if json.Unmarshal(raw, &level) == nil {
		switch level {
		case "low", "medium", "high", "xhigh", "max":
			return pickerPointer(level)
		}
	}
	return pickerPointer("high")
}

func (m *MainPrint) NativeModelPicker(ctx context.Context, standalone bool) (tui.ModelPicker, error) {
	var result tui.ModelPicker
	receipt, err := m.Owner.Submit(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if err := m.initializePicker(scope); err != nil {
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
		var override json.RawMessage = []byte("null")
		if m.modelPicker.rawModel != nil {
			override = quoteMain(*m.modelPicker.rawModel)
		}
		selector, err := m.nativeModelSelection(ctx, scope, modelselection.Options{Environment: env, Settings: merged, Override: override})
		if err != nil {
			return err
		}
		global, err := m.Global.Get()
		if err != nil {
			return err
		}
		options, err := nativePickerOptions(env, merged, global, selector, m.modelPicker.initial)
		if err != nil {
			return err
		}
		initialLabel, err := nativePickerDisplay(selector, m.modelPicker.rawModel)
		if err != nil {
			return err
		}
		if m.modelPicker.rawModel != nil {
			found := false
			for _, option := range options {
				found = found || pickerSame(option.Value, m.modelPicker.rawModel)
			}
			if !found {
				options = append(options, tui.ModelOption{Value: pickerClone(m.modelPicker.rawModel), Label: initialLabel, Description: "Current model"})
			}
		}
		for i := range options {
			optionModel := options[i].Value
			if optionModel != nil && *optionModel == "__NO_PREFERENCE__" {
				optionModel = nil
			}
			resolved, err := nativePickerResolve(selector, optionModel)
			if err != nil {
				return err
			}
			options[i].SupportsEffort, options[i].SupportsMax, options[i].DefaultEffort, err = nativePickerEffort(selector, resolved, env)
			if err != nil {
				return err
			}
		}
		m.modelPicker.next++
		result = tui.ModelPicker{ID: fmt.Sprintf("model-picker-%d", m.modelPicker.next), Initial: pickerClone(m.modelPicker.rawModel), InitialLabel: initialLabel, Options: options, Effort: pickerEffortLevel(m.modelPicker.effort), Standalone: standalone}
		pending := cloneNativePicker(result)
		m.modelPicker.pending = &pending
		return nil
	})
	if err != nil {
		return result, err
	}
	return result, receipt.Wait()
}

func cloneNativePicker(picker tui.ModelPicker) tui.ModelPicker {
	picker.Initial = pickerClone(picker.Initial)
	picker.SessionModel = pickerClone(picker.SessionModel)
	picker.Effort = pickerClone(picker.Effort)
	picker.Options = append([]tui.ModelOption(nil), picker.Options...)
	for i := range picker.Options {
		picker.Options[i].Value = pickerClone(picker.Options[i].Value)
	}
	return picker
}

func nativePickerEffort(selector *modelselection.Selector, model string, env map[string]string) (bool, bool, string, error) {
	supports, max, _ := requestcontext.NativeModelPickerEffort(model, env)
	canonicalRaw, err := selector.Canonical(quoteMain(model))
	if err != nil {
		return false, false, "", err
	}
	canonical, err := decodeNativeModel(canonicalRaw)
	if err != nil {
		return false, false, "", err
	}
	_, _, defaultLevel := requestcontext.NativeModelPickerEffort(canonical, map[string]string{})
	return supports, max, defaultLevel, nil
}

func nativePickerResolve(selector *modelselection.Selector, model *string) (string, error) {
	raw := selector.DefaultSetting()
	if model != nil {
		raw = quoteMain(*model)
	}
	parsed, err := selector.Parse(raw)
	if err != nil {
		return "", err
	}
	return decodeNativeModel(parsed)
}
func nativePickerDisplay(selector *modelselection.Selector, model *string) (string, error) {
	resolved, err := nativePickerResolve(selector, model)
	if err != nil {
		return "", err
	}
	if model == nil {
		return "Default (" + resolved + ")", nil
	}
	if *model == resolved {
		return resolved, nil
	}
	return *model + " (" + resolved + ")", nil
}

// ApplyNativeModelPicker retains Source raw aliases/null, persists the exact
// user settings fields, and updates the shared SDK host's next ASK snapshot.
func (m *MainPrint) ApplyNativeModelPicker(ctx context.Context, id string, result tui.ModelPickerResult) (string, error) {
	var notice string
	receipt, err := m.Owner.Submit(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if err := m.initializeNativeModelCommandState(scope); err != nil {
			return err
		}
		picker := m.modelPicker.pending
		if picker == nil || picker.ID != id {
			return mainCap("stale native model picker response")
		}
		m.modelPicker.pending = nil
		if result.Cancelled {
			if picker.Standalone {
				label, err := m.pickerLabel(m.nativeModelSelector, m.modelPicker.rawModel)
				if err != nil {
					return err
				}
				notice = "Kept model as " + label
			}
			return nil
		}
		var selected *tui.ModelOption
		for i := range picker.Options {
			if pickerSame(picker.Options[i].Value, result.Model) {
				selected = &picker.Options[i]
				break
			}
		}
		if selected == nil {
			return mainCap("model picker selection outside original options")
		}
		if result.HasToggledEffort && result.Effort == nil {
			return mainCap("model picker effort callback without supported option")
		}
		if result.Effort != nil {
			switch *result.Effort {
			case "low", "medium", "high", "xhigh", "max":
			default:
				return mainCap("model picker unknown effort callback")
			}
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
		override := json.RawMessage("null")
		if result.Model != nil {
			override = quoteMain(*result.Model)
		}
		selector, err := m.nativeModelSelection(ctx, scope, modelselection.Options{Environment: env, Settings: merged, Override: override})
		if err != nil {
			return err
		}
		resolved, err := nativePickerResolve(selector, result.Model)
		if err != nil {
			return err
		}
		effort := append(json.RawMessage(nil), m.modelPicker.effort...)
		if !picker.SkipSettingsWrite {
			user, err := m.Startup.GetSettingsForSource("userSettings")
			if err != nil {
				return err
			}
			hadExplicit := user != nil && user.Get("effortLevel") != nil || result.HasToggledEffort
			effort = nil
			if result.Effort != nil && (hadExplicit || *result.Effort != selected.DefaultEffort) {
				effort = quoteMain(*result.Effort)
			}
			if result.Effort != nil && effort != nil && (*result.Effort == "low" || *result.Effort == "medium" || *result.Effort == "high") {
				if err = m.writePickerSetting(clisettings.Patch{Object: protocol.NewJSONObject(protocol.JSONField{Name: "effortLevel", Value: effort})}); err != nil {
					return err
				}
			}
		}
		if !pickerSame(m.modelPicker.rawModel, result.Model) {
			patch := clisettings.Patch{Object: protocol.NewJSONObject(protocol.JSONField{Name: "model", Value: override})}
			if result.Model == nil {
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
		if err = m.Host.SetInferenceModel(scope, resolved, effort, !picker.SkipSettingsWrite); err != nil {
			return err
		}
		m.modelPicker.rawModel = pickerClone(result.Model)
		m.modelPicker.effort = effort
		m.nativeModelSelector = selector
		m.options.Arguments.Model = resolved
		if picker.Standalone {
			label, err := m.pickerLabel(selector, result.Model)
			if err != nil {
				return err
			}
			notice = "Set model to " + label
			if result.HasToggledEffort && selected.SupportsEffort && result.Effort != nil {
				notice += " with " + *result.Effort + " effort"
			}
		} else {
			notice, err = nativePickerDisplay(selector, result.Model)
			if err != nil {
				return err
			}
			notice = "Model set to " + notice
		}
		suffix, err := m.applyNativeSelectedModelState(ctx, scope, result.Model, selector, env, picker.Standalone)
		if err != nil {
			return err
		}
		if picker.Standalone {
			notice += suffix
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return notice, receipt.Wait()
}
func (m *MainPrint) writePickerSetting(patch clisettings.Patch) error {
	result, err := m.settings.UpdateEditableSource("userSettings", "", patch)
	if err != nil {
		return err
	}
	return result.Error
}

func (m *MainPrint) pickerLabel(selector *modelselection.Selector, model *string) (string, error) {
	resolved, err := nativePickerResolve(selector, model)
	if err != nil {
		return "", err
	}
	label, err := nativePickerMarketing(selector, resolved)
	if err != nil {
		return "", err
	}
	if label == "" {
		label = resolved
	}
	if model == nil {
		label += " (default)"
	}
	if model != nil && *model == "opusplan" {
		opus, err := nativePickerResolve(selector, pickerPointer("opus"))
		if err != nil {
			return "", err
		}
		o, err := nativePickerMarketing(selector, opus)
		if err != nil {
			return "", err
		}
		if o == "" {
			o = opus
		}
		label = o + " in plan mode, else " + label
	}
	return label, nil
}

func nativePickerMarketing(selector *modelselection.Selector, model string) (string, error) {
	return selector.PublicModelDisplayName(quoteMain(model))
}
