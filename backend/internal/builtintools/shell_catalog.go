package builtintools

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
)

func (s *Service) shellCatalogNames(names []string) ([]string, error) {
	if !windowshell.IsWindows(s.options.Platform) && (s.options.Shells == nil || !windowshell.IsWindows(s.options.Shells.Platform())) {
		return names, nil
	}
	if s.options.Shells == nil {
		return nil, capability("actual Windows shell discovery for native tool catalog")
	}
	view, ready := s.options.Shells.Snapshot()
	if !ready {
		return nil, capability("owned Windows shell discovery before native tool catalog")
	}
	filtered := make([]string, 0, len(names)+1)
	powerShell := view.Availability.PowerShell && (!skillSimple(s.options.Environment["CLAUDE_CODE_SIMPLE"]) || !view.Availability.Bash)
	inserted := false
	for _, name := range names {
		if name == "Bash" && !view.Availability.Bash {
			continue
		}
		if name == "ListMcpResourcesTool" && powerShell {
			filtered = append(filtered, "PowerShell")
			inserted = true
		}
		filtered = append(filtered, name)
	}
	if powerShell && !inserted {
		filtered = append(filtered, "PowerShell")
	}
	return filtered, nil
}
