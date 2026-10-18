package mcptools

import (
	"context"
	"errors"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// ReconnectTerminal is the useMcpReconnect callback. Its original store read
// selects the current client's config; failure/needs-auth are returned client
// states, not rejected promises. SDK/CLI control keeps its separate contract.
func (s *Service) ReconnectTerminal(ctx context.Context, scope *cooperative.Scope, name string) error {
	if s == nil || scope == nil || !scope.BelongsTo(s.options.Owner) || s.agentResourceSnapshot {
		return cooperative.ErrRestricted
	}
	var config protocol.JSONObject
	index := -1
	for i, peer := range s.connections {
		if peer.Name == name {
			config = peer.Config.Clone()
			index = i
			break
		}
	}
	if index < 0 {
		return errors.New("MCP server " + name + " not found")
	}
	s.retireInteractivePeer(name)
	future, err := native(s, scope, func() (interactivePeer, error) {
		if err := s.options.Runtime.Disconnect(ctx, name, config); err != nil {
			return interactivePeer{}, err
		}
		peer, err := s.options.Runtime.ConnectTransport(ctx, name, config)
		if err != nil {
			return interactivePeer{}, err
		}
		result := interactivePeer{connection: peer}
		if peer.Status != "connected" || peer.Client == nil {
			return result, nil
		}
		var toolsError, promptsError error
		var group sync.WaitGroup
		group.Add(3)
		go func() {
			defer group.Done()
			result.connection, toolsError = s.options.Runtime.RefreshConnection(ctx, peer)
		}()
		go func() { defer group.Done(); result.prompts, promptsError = s.options.Runtime.FetchPrompts(ctx, peer) }()
		go func() { defer group.Done(); result.resources = fetchSkillResources(ctx, peer) }()
		group.Wait()
		return result, errors.Join(toolsError, promptsError)
	})
	if err != nil {
		return err
	}
	result, err := future.Await(scope)
	if err != nil {
		return err
	}
	peer := result.connection
	if index >= len(s.connections) || s.connections[index].Name != name {
		return cooperative.ErrRestricted
	}
	s.connections[index] = peer
	delete(s.authenticationTools, name)
	delete(s.promptCommands, name)
	delete(s.skillResources, name)
	if peer.Status == "connected" && peer.Client != nil {
		if err := s.installPromptCommands(scope, peer, result.prompts); err != nil {
			return err
		}
		if err := s.installSkillResources(scope, peer, result.resources); err != nil {
			return err
		}
		if err := s.installToolNotifications(scope, peer); err != nil {
			return err
		}
	}
	return nil
}
