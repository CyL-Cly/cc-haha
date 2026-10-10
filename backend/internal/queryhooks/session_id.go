package queryhooks

// mainSessionID reads the activated Source bootstrap identity. A startup SID
// is only the fallback for older finite callers without a runtime controller.
func (s *Service) mainSessionID() string {
	if s.options.CurrentSessionID != nil {
		return s.options.CurrentSessionID()
	}
	return s.options.MainSessionID
}
