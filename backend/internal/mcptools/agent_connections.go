package mcptools

import (
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// AgentResourceSnapshot is minted on the parent's actual Owner. Source copies
// mcpResources into agentOptions; initializing an Agent does not list resources
// again, including on newly connected inline peers.
type AgentResourceSnapshot struct {
	producer *Service
	rows     map[string]skillResourceCatalogue
}

func (s *Service) AgentResources(scope *cooperative.Scope) (*AgentResourceSnapshot, error) {
	if !scope.BelongsTo(s.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	out := &AgentResourceSnapshot{producer: s, rows: map[string]skillResourceCatalogue{}}
	for name, catalogue := range s.skillResources {
		catalogue.rows = cloneResourceRows(catalogue.rows)
		catalogue.unsubscribe = nil
		out.rows[name] = catalogue
	}
	return out, nil
}

func cloneResourceRows(rows []protocol.JSONObject) []protocol.JSONObject {
	out := make([]protocol.JSONObject, len(rows))
	for i, row := range rows {
		out[i] = row.Clone()
	}
	return out
}

// AgentConnections copies the actual parent clients, not reconstructed server
// descriptors. The recipient's runtime must share the same process graph.
func (s *Service) AgentConnections(scope *cooperative.Scope) ([]mcpruntime.Connection, error) {
	if !scope.BelongsTo(s.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	out := make([]mcpruntime.Connection, len(s.connections))
	for i, peer := range s.connections {
		peer.Config = peer.Config.Clone()
		peer.Tools = cloneResourceRows(peer.Tools)
		out[i] = peer
	}
	return out, nil
}
func (s *Service) InstallAgentConnections(scope *cooperative.Scope, inherited, additional []mcpruntime.Connection, resources *AgentResourceSnapshot) error {
	if !scope.BelongsTo(s.options.Owner) {
		return cooperative.ErrRestricted
	}
	peers := append(append([]mcpruntime.Connection{}, inherited...), additional...)
	for _, peer := range peers {
		if !s.options.Runtime.OwnsConnection(peer) {
			return errors.New("Agent MCP connection is not owned by the shared native process graph")
		}
	}
	if resources == nil || resources.producer == nil {
		return errors.New("Agent MCP resources require the actual parent resource snapshot")
	}
	for _, peer := range inherited {
		if !resources.producer.options.Runtime.OwnsConnection(peer) {
			return errors.New("Agent resource snapshot does not own its inherited peer")
		}
	}
	s.connections = peers
	s.skillResources = map[string]skillResourceCatalogue{}
	for name, catalogue := range resources.rows {
		catalogue.rows = cloneResourceRows(catalogue.rows)
		s.skillResources[name] = catalogue
	}
	s.agentResourceSnapshot = true
	return nil
}
