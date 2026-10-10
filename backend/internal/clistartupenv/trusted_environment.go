package clistartupenv

// CaptureSpawnEnvironment is the original applySafe entry capture. A native
// host calls it before applying any settings, retaining the desktop's launch
// key protection throughout later post-trust settings notifications.
func (s *Service) CaptureSpawnEnvironment() {
	if s.ccdCaptured {
		return
	}
	s.ccdCaptured = true
	if equalString(s.options.Environment.Get("CLAUDE_CODE_ENTRYPOINT"), "claude-desktop") {
		s.ccdKeys = map[string]bool{}
		for _, property := range s.options.Environment.Snapshot().OwnProperties() {
			s.ccdKeys[string(property.NameJSON)] = true
		}
	}
}

// ApplyConfigEnvironmentVariables is the post-trust Source assignment stage.
// It is additive: removed settings keys deliberately remain in process.env.
// The owning host clears and rebuilds its actual transports after completion.
func (s *Service) ApplyConfigEnvironmentVariables() error {
	if err := s.checkHostControls(); err != nil {
		return err
	}
	global, err := s.options.Global.Get()
	if err != nil {
		return err
	}
	if err = s.apply(envFrom(global)); err != nil {
		return err
	}
	merged, err := s.mergedEnv()
	if err != nil {
		return err
	}
	if err = s.apply(merged); err != nil {
		return err
	}
	cc, err := s.ccHahaEnv()
	if err != nil {
		return err
	}
	return s.apply(cc)
}
