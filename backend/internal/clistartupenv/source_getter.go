package clistartupenv

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

// GetSettingsForSource exposes the existing real source-cache getter. The
// enclosing host owns admission, like GetInitialSettings; this method does not
// introduce policy data, reset caches, or bypass the finite host controls.
func (s *Service) GetSettingsForSource(source string) (*protocol.JSONObject, error) {
	if err := s.checkHostControls(); err != nil {
		return nil, err
	}
	if !validSource(source) {
		return nil, &CapabilityError{"setting source " + source}
	}
	return s.getSource(source)
}
