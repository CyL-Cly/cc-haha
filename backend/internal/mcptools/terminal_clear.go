package mcptools

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

// ClearInteractiveCatalog mirrors clearConversation's empty AppState MCP
// projection, followed by useManageMCPConnections' session-ID effects. Those
// effects retain connectToServer/fetch memoized peers and only cancel obsolete
// configuration publication. They do not disconnect every live server.
func (s *Service) ClearInteractiveCatalog(scope *cooperative.Scope, changed func(*cooperative.Scope) error) error {
	if scope == nil || !scope.BelongsTo(s.options.Owner) || s.agentResourceSnapshot {
		return cooperative.ErrRestricted
	}
	s.discoveryGeneration++
	s.connections = nil
	s.authenticationTools = nil
	s.sdkControlAppClients = nil
	s.failures = nil
	// Prompt/resource rows remain internal per-peer caches. Their public
	// consumers select only current connections, just like Source fetch caches.
	if changed != nil {
		if err := changed(scope); err != nil {
			return err
		}
	}
	_, err := s.BeginInteractiveDiscovery(scope, changed)
	return err
}
