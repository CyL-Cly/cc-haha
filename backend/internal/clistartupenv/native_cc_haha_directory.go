package clistartupenv

import "path/filepath"

func (s *Service) ccHahaConfigDirectory() (string, error) {
	if s.options.NativeCcHahaConfigDirectory == nil {
		return s.options.ConfigHome, nil
	}
	if !resolvedNativePolicyProfile(s.options.Host.Profile) {
		return "", &CapabilityError{"native cc-haha directory producer"}
	}
	directory, err := s.options.NativeCcHahaConfigDirectory()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(directory) {
		return "", &CapabilityError{"unresolved native cc-haha filesystem directory"}
	}
	return directory, nil
}
