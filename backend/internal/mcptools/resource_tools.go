package mcptools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Service) ResourcesAvailable(scope *cooperative.Scope) (bool, error) {
	if !scope.BelongsTo(s.options.Owner) {
		return false, cooperative.ErrRestricted
	}
	for _, peer := range s.connections {
		if peer.Status == "connected" && peer.Client != nil && resourceSupport(peer) {
			return true, nil
		}
	}
	return false, nil
}
func resourceSupport(peer mcpruntime.Connection) bool {
	if peer.Client == nil {
		return false
	}
	v := peer.Client.Capabilities().Get("resources")
	return len(v) > 0 && string(v) != "null" && string(v) != "false"
}
func (s *Service) resourcePeerNames() []string {
	names := []string{}
	for _, peer := range s.connections {
		names = append(names, peer.Name)
	}
	if s.computerUse != nil {
		names = append(names, ComputerUseServerName)
	}
	return names
}
func (s *Service) ensureResourcePeer(ctx context.Context, scope *cooperative.Scope, previous mcpruntime.Connection) (mcpruntime.Connection, error) {
	future, e := native(s, scope, func() (mcpruntime.Connection, error) {
		return s.options.Runtime.RefreshConnection(ctx, previous)
	})
	if e != nil {
		return mcpruntime.Connection{}, e
	}
	fresh, e := future.Await(scope)
	if e != nil {
		return fresh, e
	}
	for i := range s.connections {
		if s.connections[i].Name == previous.Name {
			s.connections[i] = fresh
			break
		}
	}
	if fresh.Status != "connected" || fresh.Client == nil {
		if fresh.Failure != nil {
			return fresh, fresh.Failure
		}
		return fresh, fmt.Errorf("Server \"%s\" is not connected", fresh.Name)
	}
	return fresh, nil
}

func (s *Service) ListResourceTool(ctx context.Context, scope *cooperative.Scope, server *string) (json.RawMessage, error) {
	if !scope.BelongsTo(s.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	peers := append([]mcpruntime.Connection{}, s.connections...)
	target := ""
	if server != nil {
		target = *server
	}
	selected := []mcpruntime.Connection{}
	for _, peer := range peers {
		if target == "" || peer.Name == target {
			selected = append(selected, peer)
		}
	}
	if target != "" && len(selected) == 0 && !(target == ComputerUseServerName && s.computerUse != nil) {
		return nil, fmt.Errorf("Server \"%s\" not found. Available servers: %s", target, strings.Join(s.resourcePeerNames(), ", "))
	}
	rows := []protocol.JSONObject{}
	type pendingResourcePeer struct {
		future *ownerasync.OwnerFuture[resourcePeerResult]
	}
	pending := []pendingResourcePeer{}
	for _, peer := range selected {
		if peer.Status != "connected" || peer.Client == nil {
			continue
		}
		previous := s.skillResources[peer.Name]
		future, e := native(s, scope, func() (resourcePeerResult, error) {
			fresh, e := s.options.Runtime.RefreshConnection(ctx, peer)
			if e != nil {
				return resourcePeerResult{}, e
			}
			if fresh.Status != "connected" || fresh.Client == nil {
				if fresh.Failure != nil {
					return resourcePeerResult{}, fresh.Failure
				}
				return resourcePeerResult{}, fmt.Errorf("Server \"%s\" is not connected", fresh.Name)
			}
			if previous.client == fresh.Client && previous.fresh {
				return resourcePeerResult{peer: fresh, rows: previous.rows}, nil
			}
			return resourcePeerResult{peer: fresh, rows: fetchSkillResources(ctx, fresh), refreshed: true}, nil
		})
		if e != nil {
			return nil, e
		}
		pending = append(pending, pendingResourcePeer{future: future})
	}
	for _, item := range pending {
		result, e := item.future.Await(scope)
		if e != nil {
			if ctx.Err() != nil {
				return nil, context.Cause(ctx)
			}
			continue
		}
		for i := range s.connections {
			if s.connections[i].Name == result.peer.Name {
				s.connections[i] = result.peer
				break
			}
		}
		if result.refreshed {
			if e := s.installSkillResources(scope, result.peer, result.rows); e != nil {
				return nil, e
			}
		}
		for _, resource := range result.rows {
			rows = append(rows, resource.Clone())
		}
	}
	return raw(rows), nil
}

type resourcePeerResult struct {
	peer      mcpruntime.Connection
	rows      []protocol.JSONObject
	refreshed bool
}

func (s *Service) ReadResourceToolRaw(ctx context.Context, scope *cooperative.Scope, server, uri string) (protocol.JSONObject, error) {
	if !scope.BelongsTo(s.options.Owner) {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	for _, peer := range append([]mcpruntime.Connection{}, s.connections...) {
		if peer.Name != server {
			continue
		}
		if peer.Status != "connected" || peer.Client == nil {
			return protocol.JSONObject{}, fmt.Errorf("Server \"%s\" is not connected", server)
		}
		if !resourceSupport(peer) {
			return protocol.JSONObject{}, fmt.Errorf("Server \"%s\" does not support resources", server)
		}
		fresh, e := s.ensureResourcePeer(ctx, scope, peer)
		if e != nil {
			return protocol.JSONObject{}, e
		}
		future, e := native(s, scope, func() (protocol.JSONObject, error) { return fresh.Client.ReadResource(ctx, uri) })
		if e != nil {
			return protocol.JSONObject{}, e
		}
		return future.Await(scope)
	}
	if server == ComputerUseServerName && s.computerUse != nil {
		return protocol.JSONObject{}, fmt.Errorf("Server \"%s\" does not support resources", server)
	}
	return protocol.JSONObject{}, fmt.Errorf("Server \"%s\" not found. Available servers: %s", server, strings.Join(s.resourcePeerNames(), ", "))
}
