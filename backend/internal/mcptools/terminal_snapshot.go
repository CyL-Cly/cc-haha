package mcptools

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// TerminalCatalogue is a detached read of the same AppState-side MCP peers,
// tools, resources and prompts. It neither reconnects nor sends a list request.
func (s *Service) TerminalCatalogue(ctx context.Context, scope *cooperative.Scope) ([]protocol.JSONObject, error) {
	rows, err := s.ServerStatuses(ctx, scope)
	if err != nil {
		return nil, err
	}
	peers := map[string]mcpruntime.Connection{}
	for _, peer := range s.sdkControlAppClients {
		peers[peer.Name] = peer
	}
	for _, peer := range s.connections {
		peers[peer.Name] = peer
	}
	for _, name := range s.sdkControlOrder {
		if _, found := peers[name]; !found {
			if peer, found := s.sdkControlClients[name]; found {
				peers[name] = peer
			}
		}
	}
	for i := range rows {
		row := rows[i].Clone()
		peer, found := peers[row.String("name")]
		if found {
			config := object(row.Get("config"))
			if value := peer.Config.Get("scope"); value != nil {
				config.Set("scope", value)
			}
			row.Set("config", raw(config))
			tools := []protocol.JSONObject{}
			if row.String("status") == "connected" {
				for _, tool := range peer.Tools {
					tools = append(tools, tool.Clone())
				}
				if peer.Config.String("type") == "sdk" && s.sdkStatusToolsHidden[peer.Name] {
					tools = nil
				}
				if peer.Client != nil {
					row.Set("capabilities", raw(peer.Client.Capabilities()))
				}
			}
			row.Set("toolMetadata", raw(tools))
			resources, prompts := []protocol.JSONObject{}, []protocol.JSONObject{}
			if current, found := s.skillResources[peer.Name]; found && current.client == peer.Client {
				resources = cloneResourceRows(current.rows)
			}
			if current, found := s.promptCommands[peer.Name]; found && current.peer.Client == peer.Client {
				prompts = cloneResourceRows(current.rows)
			}
			row.Set("resources", raw(resources))
			row.Set("prompts", raw(prompts))
		}
		rows[i] = row
	}
	return rows, nil
}
