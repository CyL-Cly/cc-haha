package clistartupenv

import "github.com/NanmiCoder/cc-haha/backend/internal/clisettings"

// OwnsNativeTaskEnvironment checks the concrete process services installed by
// the native factory. Equal JSON or another environment cannot replace them.
// This pure identity check does not reread settings or mutate a startup cache.
func (s *Service) OwnsNativeTaskEnvironment(settings *clisettings.Service, state *clisettings.State, environment *Environment) bool {
	return s != nil && resolvedNativePolicyProfile(s.options.Host.Profile) && settings != nil && state != nil && environment != nil && s.options.Settings == settings && s.options.SettingsState == state && s.options.Environment == environment && settings.UsesNativeUserSettingsPath(state)
}
