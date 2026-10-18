package mcpruntime

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type resourceDiscovery struct {
	done chan struct{}
	rows []protocol.JSONObject
}

// FetchResources retains Source fetchResourcesForClient's shared pending
// Promise and 20-entry LRU. /clear resets app projections, not these actual
// per-peer fetches; a second effect must not duplicate a held resources/list.
func (s *Service) FetchResources(ctx context.Context, peer Connection) ([]protocol.JSONObject, error) {
	if !s.OwnsConnection(peer) {
		return nil, errors.New("MCP resource peer belongs to a different process graph")
	}
	if peer.Status != "connected" || peer.Client == nil || peer.Client.Capabilities().Get("resources") == nil {
		return []protocol.JSONObject{}, nil
	}
	owner := peer.runtime
	key := owner.key(peer.Name, peer.Config)
	owner.mu.Lock()
	if owner.closed {
		owner.mu.Unlock()
		return nil, context.Canceled
	}
	if owner.resourceCache == nil {
		owner.resourceCache = map[string]*resourceDiscovery{}
		owner.resourceReads = map[*resourceDiscovery]bool{}
	}
	read := owner.resourceCache[key]
	if read == nil {
		read = &resourceDiscovery{done: make(chan struct{}), rows: []protocol.JSONObject{}}
		owner.resourceCache[key] = read
		owner.resourceReads[read] = true
		go func() {
			defer func() { owner.mu.Lock(); delete(owner.resourceReads, read); owner.mu.Unlock() }()
			result, err := peer.Client.ListResources(context.Background(), nil)
			if err == nil {
				_ = json.Unmarshal(result.Get("resources"), &read.rows)
			}
			for i := range read.rows {
				read.rows[i] = read.rows[i].Clone()
				read.rows[i].Set("server", resultRaw(peer.Name))
			}
			close(read.done)
		}()
	}
	if at := slices.Index(owner.resourceOrder, key); at >= 0 {
		owner.resourceOrder = append(owner.resourceOrder[:at], owner.resourceOrder[at+1:]...)
	}
	owner.resourceOrder = append(owner.resourceOrder, key)
	if len(owner.resourceOrder) > 20 {
		delete(owner.resourceCache, owner.resourceOrder[0])
		owner.resourceOrder = owner.resourceOrder[1:]
	}
	owner.mu.Unlock()
	select {
	case <-read.done:
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
	return clonePromptRows(read.rows), nil
}
func (peer Connection) FetchResources(ctx context.Context) ([]protocol.JSONObject, error) {
	if peer.runtime == nil {
		return nil, errors.New("MCP resource request requires its actual runtime")
	}
	return peer.runtime.FetchResources(ctx, peer)
}
func (s *Service) InvalidateResources(peer Connection) error {
	if !s.OwnsConnection(peer) {
		return errors.New("MCP resource invalidation belongs to a different process graph")
	}
	owner := peer.runtime
	owner.mu.Lock()
	owner.forgetResourcesLocked(owner.key(peer.Name, peer.Config))
	owner.mu.Unlock()
	return nil
}
func (s *Service) forgetResourcesLocked(key string) {
	delete(s.resourceCache, key)
	if at := slices.Index(s.resourceOrder, key); at >= 0 {
		s.resourceOrder = append(s.resourceOrder[:at], s.resourceOrder[at+1:]...)
	}
}
