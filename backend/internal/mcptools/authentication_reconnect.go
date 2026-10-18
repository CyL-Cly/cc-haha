package mcptools

import (
	"context"
	"errors"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
)

// Authentication captures the original tool's server configuration. Reconnect
// runs even when the Agent call context has a no-op setAppState callback.
func (s *Service) reconnectAuthenticationTool(ctx context.Context, scope *cooperative.Scope, peer mcpruntime.Connection, publish func() bool) (int, error) {
	if s == nil || scope == nil || !scope.BelongsTo(s.options.Owner) || s.agentResourceSnapshot {
		return 0, cooperative.ErrRestricted
	}
	future, err := native(s, scope, func() (interactivePeer, error) {
		if err := s.options.Runtime.Disconnect(ctx, peer.Name, peer.Config); err != nil {
			return interactivePeer{}, err
		}
		current, err := s.options.Runtime.ConnectTransport(ctx, peer.Name, peer.Config)
		if err != nil {
			return interactivePeer{}, err
		}
		result := interactivePeer{connection: current}
		if current.Status != "connected" || current.Client == nil {
			return result, nil
		}
		var toolsError, promptsError error
		var group sync.WaitGroup
		group.Add(3)
		go func() {
			defer group.Done()
			result.connection, toolsError = s.options.Runtime.RefreshConnection(ctx, current)
		}()
		go func() {
			defer group.Done()
			result.prompts, promptsError = s.options.Runtime.FetchPrompts(ctx, current)
		}()
		go func() { defer group.Done(); result.resources = fetchSkillResources(ctx, current) }()
		group.Wait()
		return result, errors.Join(toolsError, promptsError)
	})
	if err != nil {
		return 0, err
	}
	result, err := future.Await(scope)
	if err != nil {
		return 0, err
	}
	count := len(result.connection.Tools)
	if !publish() {
		return count, nil
	}
	s.retireInteractivePeer(peer.Name)
	for i, current := range s.connections {
		if current.Name == peer.Name {
			s.connections[i] = result.connection
		}
	}
	delete(s.authenticationTools, peer.Name)
	delete(s.promptCommands, peer.Name)
	delete(s.skillResources, peer.Name)
	if current := result.connection; current.Status == "connected" && current.Client != nil {
		if err := s.installPromptCommands(scope, current, result.prompts); err != nil {
			return count, err
		}
		if err := s.installSkillResources(scope, current, result.resources); err != nil {
			return count, err
		}
		if err := s.installToolNotifications(scope, current); err != nil {
			return count, err
		}
	}
	if s.options.OnAuthenticationChanged != nil {
		return count, s.options.OnAuthenticationChanged(scope)
	}
	return count, nil
}
