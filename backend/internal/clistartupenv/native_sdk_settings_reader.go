package clistartupenv

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// NativeSDKSettingsReader retains the original source and merged caches. Only
// the concrete native SDK environment consumer binds this reader, before its
// first directory update; ordinary startup getters keep their full admission.
type NativeSDKSettingsReader struct{ service *Service }

func (reader *NativeSDKSettingsReader) UsesSettingsContext(startup *Service, settings *clisettings.Service, state *clisettings.State, environment *Environment) bool {
	return reader != nil && reader.service != nil && reader.service == startup && settings != nil && state != nil && environment != nil && reader.service.options.Settings == settings && reader.service.options.SettingsState == state && reader.service.options.Environment == environment && settings.UsesNativeUserSettingsPath(state)
}

func (s *Service) BindNativeSDKSettingsReader(settings *clisettings.Service, state *clisettings.State, environment *Environment) (*NativeSDKSettingsReader, error) {
	if s == nil || !resolvedNativePolicyProfile(s.options.Host.Profile) || settings == nil || state == nil || environment == nil || s.options.Settings != settings || s.options.SettingsState != state || s.options.Environment != environment || !settings.UsesNativeUserSettingsPath(state) {
		return nil, &CapabilityError{"actual native SDK source services"}
	}
	if err := s.checkHostControls(); err != nil {
		return nil, err
	}
	return &NativeSDKSettingsReader{service: s}, nil
}

func (reader *NativeSDKSettingsReader) check() error {
	if reader == nil || reader.service == nil {
		return &CapabilityError{"unbound native SDK source reader"}
	}
	return reader.service.checkNativeAgentSettingsControls()
}

func (reader *NativeSDKSettingsReader) InitialSettings() (*protocol.JSONObject, error) {
	if err := reader.check(); err != nil {
		return nil, err
	}
	return reader.service.getInitialSettings()
}

func (reader *NativeSDKSettingsReader) SourceSettings(source string) (*protocol.JSONObject, error) {
	if err := reader.check(); err != nil {
		return nil, err
	}
	if !validSource(source) {
		return nil, &CapabilityError{"setting source " + source}
	}
	return reader.service.getSource(source)
}

func (reader *NativeSDKSettingsReader) SettingsWithSources() (protocol.JSONObject, error) {
	if err := reader.check(); err != nil {
		return protocol.JSONObject{}, err
	}
	return reader.service.getSettingsWithSources()
}
