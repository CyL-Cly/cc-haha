package clibootstrap

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func (m *MainPrint) nativeSDKGetSettings(scope *cooperative.Scope) (protocol.JSONObject, error) {
	if !scope.BelongsTo(m.Owner) || m.Host == nil || m.Startup == nil {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	model, err := m.Host.InferenceModel(scope)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	effort, _, err := m.Host.InferenceEffort(scope)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	environment, err := m.pickerEnvironment()
	if err != nil {
		return protocol.JSONObject{}, err
	}
	applied := protocol.NewJSONObject(protocol.JSONField{Name: "model", Value: quoteMain(model)}, protocol.JSONField{Name: "effort", Value: requestcontext.NativeSDKAppliedEffort(model, effort, environment)})
	before, err := m.Startup.GetInitialSettings()
	if err != nil {
		return protocol.JSONObject{}, err
	}
	settings, err := m.Startup.GetSettingsWithSources()
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if err = m.Host.AcceptNativeSettingsReadRefresh(scope, before); err != nil {
		return protocol.JSONObject{}, err
	}
	settings.Set("applied", rawMain(applied))
	return settings, nil
}
