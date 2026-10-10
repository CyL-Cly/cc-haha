package mcptools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type skillResourceCatalogue struct {
	client      *mcpclient.Client
	rows        []protocol.JSONObject
	unsubscribe func()
	fresh       bool
}

func (s *Service) prefetchSkillResources(scope *cooperative.Scope) error {
	if !scope.BelongsTo(s.options.Owner) {
		return cooperative.ErrRestricted
	}
	type pendingCatalogue struct {
		peer   mcpruntime.Connection
		future *ownerasync.OwnerFuture[[]protocol.JSONObject]
	}
	pending := []pendingCatalogue{}
	for _, peer := range s.connections {
		if peer.Status != "connected" || peer.Client == nil {
			continue
		}
		if old, present := s.skillResources[peer.Name]; present && old.client == peer.Client && old.fresh {
			continue
		}
		future, e := native(s, scope, func() ([]protocol.JSONObject, error) {
			return fetchSkillResources(context.Background(), peer), nil
		})
		if e != nil {
			return e
		}
		pending = append(pending, pendingCatalogue{peer: peer, future: future})
	}
	// Source starts all resource requests before awaiting their ordered results.
	for _, item := range pending {
		rows, e := item.future.Await(scope)
		if e != nil {
			return e
		}
		if e := s.installSkillResources(scope, item.peer, rows); e != nil {
			return e
		}
	}
	return nil
}

func fetchSkillResources(ctx context.Context, peer mcpruntime.Connection) []protocol.JSONObject {
	rows := []protocol.JSONObject{}
	if !resourceSupport(peer) {
		return rows
	}
	result, e := peer.Client.ListResources(ctx, nil)
	if e != nil {
		// fetchResourcesForClient catches peer errors and caches []; pagination
		// and output-schema stripping are not part of this original operation.
		return rows
	}
	if e := json.Unmarshal(result.Get("resources"), &rows); e != nil {
		return []protocol.JSONObject{}
	}
	for i := range rows {
		rows[i] = rows[i].Clone()
		rows[i].Set("server", raw(peer.Name))
	}
	return rows
}

func (s *Service) installSkillResources(scope *cooperative.Scope, peer mcpruntime.Connection, rows []protocol.JSONObject) error {
	if !scope.BelongsTo(s.options.Owner) {
		return cooperative.ErrRestricted
	}
	client, name := peer.Client, peer.Name
	old := s.skillResources[name]
	if old.client != client && old.unsubscribe != nil {
		old.unsubscribe()
	}
	if s.skillResources == nil {
		s.skillResources = map[string]skillResourceCatalogue{}
	}
	catalogue := skillResourceCatalogue{client: client, rows: rows, fresh: true}
	if old.client == client {
		catalogue.unsubscribe = old.unsubscribe
	}
	resources := object(client.Capabilities().Get("resources"))
	if string(resources.Get("listChanged")) == "true" && catalogue.unsubscribe == nil {
		unsubscribe, e := client.SubscribeNotifications(func(method string, _ protocol.JSONObject) {
			if method != "notifications/resources/list_changed" {
				return
			}
			_, _ = s.options.Owner.SubmitIndependent(context.Background(), cooperative.Input, func(current *cooperative.Scope) error {
				previous, present := s.skillResources[name]
				if !present || previous.client != client {
					return nil
				}
				// Source invalidates its fetch cache, while the current app
				// resource view remains usable until the async refresh settles.
				previous.fresh = false
				s.skillResources[name] = previous
				return s.prefetchSkillResources(current)
			})
		})
		if e != nil {
			return e
		}
		catalogue.unsubscribe = unsubscribe
	}
	s.skillResources[name] = catalogue
	return nil
}

// SkillResourceAttachment reads through the connected peer which supplied the
// original catalogue. No new connection, tool service or permission grant is
// created by the prompt expansion.
func (s *Service) SkillResourceAttachment(ctx context.Context, scope *cooperative.Scope, mention string) (json.RawMessage, error) {
	if !scope.BelongsTo(s.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	parts := strings.SplitN(mention, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, nil
	}
	server, uri := parts[0], parts[1]
	for _, peer := range s.connections {
		if peer.Name != server || peer.Status != "connected" || peer.Client == nil {
			continue
		}
		catalogue, present := s.skillResources[server]
		if !present || catalogue.client != peer.Client {
			if s.agentResourceSnapshot {
				return nil, nil
			}
			return nil, &resourceCatalogueError{server: server}
		}
		for _, resource := range catalogue.rows {
			if resource.String("uri") != uri {
				continue
			}
			future, e := native(s, scope, func() (protocol.JSONObject, error) { return peer.Client.ReadResource(ctx, uri) })
			if e != nil {
				return nil, e
			}
			content, e := future.Await(scope)
			if e != nil {
				if ctx.Err() != nil {
					return nil, context.Cause(ctx)
				}
				return nil, nil
			}
			name := resource.Get("name")
			if len(name) == 0 || string(name) == `""` || string(name) == "null" {
				name = raw(uri)
			}
			payload := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: raw("mcp_resource")}, protocol.JSONField{Name: "server", Value: raw(server)}, protocol.JSONField{Name: "uri", Value: raw(uri)}, protocol.JSONField{Name: "name", Value: name}, protocol.JSONField{Name: "description", Value: resource.Get("description")}, protocol.JSONField{Name: "content", Value: raw(content)})
			return raw(payload), nil
		}
	}
	return nil, nil
}

type resourceCatalogueError struct{ server string }

func (e *resourceCatalogueError) Error() string {
	return "native MCP resource catalogue is not initialized for connected server " + e.server
}
