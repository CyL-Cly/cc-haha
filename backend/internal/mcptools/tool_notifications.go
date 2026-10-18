package mcptools

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type toolNotification struct {
	client      *mcpclient.Client
	unsubscribe func()
}

func (s *Service) installToolNotifications(scope *cooperative.Scope, peer mcpruntime.Connection) error {
	if !scope.BelongsTo(s.options.Owner) {
		return cooperative.ErrRestricted
	}
	if s.options.OnCatalogChanged == nil || peer.Status != "connected" || peer.Client == nil {
		return nil
	}
	capabilities, _ := peer.Client.Capabilities().Object("tools")
	if string(capabilities.Get("listChanged")) != "true" {
		return nil
	}
	if s.toolNotifications == nil {
		s.toolNotifications = map[string]toolNotification{}
	}
	previous := s.toolNotifications[peer.Name]
	if previous.client == peer.Client {
		return nil
	}
	if previous.unsubscribe != nil {
		previous.unsubscribe()
	}
	unsubscribe, err := peer.Client.SubscribeNotifications(func(method string, _ protocol.JSONObject) {
		if method != "notifications/tools/list_changed" {
			return
		}
		_, _ = s.options.Owner.SubmitIndependent(context.Background(), cooperative.Input, func(next *cooperative.Scope) error {
			if s.toolNotifications[peer.Name].client != peer.Client {
				return nil
			}
			if err := s.options.Runtime.InvalidateTools(peer); err != nil {
				return err
			}
			read, err := native(s, next, func() (mcpruntime.Connection, error) {
				return s.options.Runtime.RefreshConnection(context.Background(), peer)
			})
			if err != nil {
				return err
			}
			connection, err := read.Await(next)
			if err != nil {
				return err
			}
			for index, current := range s.connections {
				if current.Name == peer.Name && current.Client == peer.Client {
					s.connections[index].Tools = cloneResourceRows(connection.Tools)
					s.connections[index].Failure = connection.Failure
					return s.options.OnCatalogChanged(next)
				}
			}
			return nil
		})
	})
	if err != nil {
		return err
	}
	s.toolNotifications[peer.Name] = toolNotification{peer.Client, unsubscribe}
	return nil
}
