package mcptools

import (
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
)

// Team workers keep their original main-session resource state. Their preset
// merges real initialized clients without adopting the subagent resource mode.
func (s *Service) InstallTeamConnections(scope *cooperative.Scope, inherited, additional []mcpruntime.Connection) error {
	if !scope.BelongsTo(s.options.Owner) {
		return cooperative.ErrRestricted
	}
	peers := append(append([]mcpruntime.Connection{}, inherited...), additional...)
	for _, peer := range peers {
		if !s.options.Runtime.OwnsConnection(peer) {
			return errors.New("Team MCP connection is not owned by the actual native process graph")
		}
	}
	s.connections = peers
	return nil
}
