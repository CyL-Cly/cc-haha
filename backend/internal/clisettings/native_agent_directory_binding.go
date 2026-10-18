package clisettings

// UsesNativeUserSettingsPath checks the immutable service construction used by
// the actual Agent source reader. A finite or fixed-path service cannot claim
// fresh native directory semantics merely by sharing the source cache.
func (s *Service) UsesNativeUserSettingsPath(state *State) bool {
	return s != nil && state != nil && s.options.Profile == NativeProcessProfile && s.options.State == state && s.options.NativeUserSettingsPath != nil
}
