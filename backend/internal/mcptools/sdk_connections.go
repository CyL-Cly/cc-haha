package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Service) SDKNeedsRefresh(scope *cooperative.Scope, configurations protocol.JSONObject) (bool, error) {
	if !scope.BelongsTo(s.options.Owner) {
		return false, cooperative.ErrRestricted
	}
	return sdkNeedsRefresh(s.connections, configurations)
}

func sdkNeedsRefresh(connections []mcpruntime.Connection, configurations protocol.JSONObject) (bool, error) {
	connected := map[string]bool{}
	for _, peer := range connections {
		if peer.Config.String("type") != "sdk" {
			continue
		}
		if configurations.Get(peer.Name) == nil || peer.Status == "pending" || peer.Status == "failed" {
			return true, nil
		}
		connected[peer.Name] = true
	}
	for _, field := range configurations.OwnProperties() {
		var name string
		if err := json.Unmarshal(field.NameJSON, &name); err != nil {
			return false, err
		}
		if !connected[name] {
			return true, nil
		}
	}
	return false, nil
}

// Notifications use only the first actual connected SDK client. They do not
// create connections or fall through to a same-name ordinary MCP service.
func (s *Service) DeliverSDKMessage(scope *cooperative.Scope, name string, message protocol.JSONObject) error {
	if !scope.BelongsTo(s.options.Owner) {
		return cooperative.ErrRestricted
	}
	for _, peer := range s.connections {
		if peer.Config.String("type") == "sdk" && peer.Name == name && peer.Status == "connected" && peer.Client != nil {
			return peer.Client.DeliverSDKMessage(message)
		}
	}
	return nil
}

// Original setupSdkMcpClients starts every current SDK handshake before waiting
// for any of them, then collects results in Object.entries order. In particular
// a held first SDK peer must not prevent a sibling's initialize/list producer.
func (s *Service) connectSDKAttempts(ctx context.Context, configs protocol.JSONObject) ([]mcpruntime.Connection, error) {
	type target struct {
		name   string
		config protocol.JSONObject
	}
	targets := []target{}
	for _, field := range configs.OwnProperties() {
		var name string
		if err := json.Unmarshal(field.NameJSON, &name); err != nil {
			return nil, err
		}
		config := object(field.Value)
		enabled, err := s.options.Runtime.ExecutionEnabled(ctx, name, config)
		if err != nil {
			return nil, err
		}
		if !enabled {
			continue
		}
		config.Set("scope", raw("dynamic"))
		targets = append(targets, target{name, config})
	}
	peers := make([]mcpruntime.Connection, len(targets))
	failures := make([]error, len(targets))
	var joined sync.WaitGroup
	for i, target := range targets {
		joined.Add(1)
		go func() {
			defer joined.Done()
			peers[i], failures[i] = s.options.Runtime.ConnectSDKAttempt(ctx, target.name, target.config)
		}()
	}
	joined.Wait()
	return peers, errors.Join(failures...)
}
