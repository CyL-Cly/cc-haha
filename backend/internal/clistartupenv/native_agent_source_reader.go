package clistartupenv

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// NativeAgentSettingsReader binds the existing source getter to the actual
// native catalogue's services before discovery starts. Only that reader uses
// a fresh ConfigDir; startup, assignments and ordinary getters retain their
// full host admission. It neither warms nor resets any settings cache.
func (s *Service) NativeAgentSettingsReader(settings *clisettings.Service, state *clisettings.State, environment *Environment) (func(string) (*protocol.JSONObject, error), error) {
	if s == nil || !resolvedNativePolicyProfile(s.options.Host.Profile) || settings == nil || state == nil || environment == nil || s.options.Settings != settings || s.options.SettingsState != state || s.options.Environment != environment || !settings.UsesNativeUserSettingsPath(state) {
		return nil, &CapabilityError{"actual native Agent source services"}
	}
	if err := s.checkHostControls(); err != nil {
		return nil, err
	}
	return func(source string) (*protocol.JSONObject, error) {
		if err := s.checkNativeAgentSettingsControls(); err != nil {
			return nil, err
		}
		if !validSource(source) {
			return nil, &CapabilityError{"setting source " + source}
		}
		return s.getSource(source)
	}, nil
}

func (s *Service) checkNativeAgentSettingsControls() error {
	for _, key := range frozenHostControls {
		if key != "CLAUDE_CONFIG_DIR" && !scalarEqual(s.hostControls[key], s.options.Environment.Get(key)) {
			return &CapabilityError{"changed filesystem/schema host " + key}
		}
	}
	return nil
}

// Agent memory's enablement uses the original merged getter independently of
// the definition and per-source caches. Bind before discovery, to those same
// native services; changing ConfigDir cannot reset a warm merged cache.
func (s *Service) NativeAgentMemorySettingsReader(settings *clisettings.Service, state *clisettings.State, environment *Environment) (func() (*protocol.JSONObject, error), func() error, error) {
	if _, err := s.NativeAgentSettingsReader(settings, state, environment); err != nil {
		return nil, nil, err
	}
	return func() (*protocol.JSONObject, error) {
		if err := s.checkNativeAgentSettingsControls(); err != nil {
			return nil, err
		}
		return s.getInitialSettings()
	}, s.checkNativeAgentSettingsControls, nil
}
