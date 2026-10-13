package builtintools

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

// These checks precede internal grants. The existing Windows runtime capability
// gate stays intact; only the same portable Source path predicate is reused.
func (s *Service) readPathSafety(c protocol.JSONObject, path string) string {
	allowUNC := s.options.Platform == "windows" && s.pathInAllowedWorkingPath(c, path)
	forms := []string{path, s.permissionResolved(path)}
	for _, form := range forms {
		if !allowUNC && (strings.HasPrefix(form, `\\`) || strings.HasPrefix(form, "//")) {
			return "unc"
		}
	}
	for _, form := range forms {
		if suspiciousHookPath(form, s.options.Platform, allowUNC) {
			return "windows"
		}
	}
	return ""
}
