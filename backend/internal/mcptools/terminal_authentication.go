package mcptools

import (
	"context"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// TerminalAuthenticationTarget reads the same live server configuration as
// the existing control/reconnect consumer, without accepting UI-prepared auth.
func (s *Service) TerminalAuthenticationTarget(ctx context.Context, scope *cooperative.Scope, name string) (protocol.JSONObject, error) {
	if s == nil || scope == nil || !scope.BelongsTo(s.options.Owner) || s.agentResourceSnapshot {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	previous := append(s.connections[:0:0], s.connections...)
	pending, err := native(s, scope, func() (protocol.JSONObject, error) {
		current, err := s.options.Configurations.ByName(ctx, name)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		if current != nil {
			return current.Clone(), nil
		}
		for _, peer := range previous {
			if peer.Name == name {
				return peer.Config.Clone(), nil
			}
		}
		return protocol.JSONObject{}, errors.New("Server not found: " + name)
	})
	if err != nil {
		return protocol.JSONObject{}, err
	}
	return pending.Await(scope)
}

// ClearTerminalAuthentication is Source revokeServerTokens followed by
// clearServerCache and the failed AppState projection. No settings toggle,
// replacement namespace, or new connection substitutes for that transition.
func (s *Service) ClearTerminalAuthentication(ctx context.Context, scope *cooperative.Scope, name string, config protocol.JSONObject) error {
	if s == nil || scope == nil || !scope.BelongsTo(s.options.Owner) || s.agentResourceSnapshot {
		return cooperative.ErrRestricted
	}
	pending, err := native(s, scope, func() (struct{}, error) {
		if err := s.options.Runtime.RevokeOAuth(ctx, name, config, false); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, nil
	})
	if err != nil {
		return err
	}
	if _, err = pending.Await(scope); err != nil {
		return err
	}
	return s.disconnectTerminalAuthentication(ctx, scope, name, config, "failed")
}

// Source revokes a Claude AI connector in its browser. Enter only clears the
// local peer cache and projects needs-auth, without touching account tokens.
func (s *Service) ClearTerminalClaudeAIAuthentication(ctx context.Context, scope *cooperative.Scope, name string, config protocol.JSONObject) error {
	if s == nil || scope == nil || !scope.BelongsTo(s.options.Owner) || s.agentResourceSnapshot || config.String("type") != "claudeai-proxy" {
		return cooperative.ErrRestricted
	}
	return s.disconnectTerminalAuthentication(ctx, scope, name, config, "needs-auth")
}

func (s *Service) disconnectTerminalAuthentication(ctx context.Context, scope *cooperative.Scope, name string, config protocol.JSONObject, status string) error {
	pending, err := native(s, scope, func() (struct{}, error) { return struct{}{}, s.options.Runtime.Disconnect(ctx, name, config) })
	if err != nil {
		return err
	}
	if _, err = pending.Await(scope); err != nil {
		return err
	}
	s.retireInteractivePeer(name)
	delete(s.authenticationTools, name)
	for i, peer := range s.connections {
		if peer.Name == name {
			peer.Status = status
			peer.Client = nil
			peer.Failure = nil
			peer.Tools = nil
			s.connections[i] = peer
		}
	}
	for i, peer := range s.sdkControlAppClients {
		if peer.Name == name {
			peer.Status = status
			peer.Client = nil
			peer.Failure = nil
			peer.Tools = nil
			s.sdkControlAppClients[i] = peer
		}
	}
	if peer, ok := s.sdkControlClients[name]; ok {
		peer.Status = status
		peer.Client = nil
		peer.Failure = nil
		peer.Tools = nil
		s.sdkControlClients[name] = peer
	}
	delete(s.skillResources, name)
	delete(s.promptCommands, name)
	return nil
}
