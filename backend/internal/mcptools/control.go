package mcptools

import (
	"context"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// ServerStatuses is the control-response projection. The init message's
// separate Status projection retains its original minimal name/status shape.
func (s *Service) ServerStatuses(ctx context.Context, scope *cooperative.Scope) ([]protocol.JSONObject, error) {
	if !scope.BelongsTo(s.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	statuses := []protocol.JSONObject{}
	peers := []mcpruntime.Connection{}
	peers = append(peers, s.sdkControlAppClients...)
	peers = append(peers, s.connections...)
	existing := map[string]bool{}
	for _, peer := range peers {
		existing[peer.Name] = true
	}
	for _, name := range s.sdkControlOrder {
		if peer, ok := s.sdkControlClients[name]; ok && !existing[name] {
			peers = append(peers, peer)
		}
	}

	for _, peer := range peers {
		enabled, err := s.options.Runtime.ExecutionEnabled(ctx, peer.Name, peer.Config)
		if err != nil {
			return nil, err
		}
		status := peer.Status
		if !enabled {
			status = "disabled"
		}
		value := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: raw(peer.Name)}, protocol.JSONField{Name: "status", Value: raw(status)})
		if status == "connected" && peer.Client != nil && !peer.SDKManaged() {
			info := peer.Client.ServerInfo()
			if info.IsObject() {
				value.Set("serverInfo", raw(info))
			}
		}
		if status == "failed" && peer.Failure != nil && !peer.SDKManaged() {
			value.Set("error", raw(peer.Failure.Error()))
		}
		config := protocol.NewJSONObject()
		kind := peer.Config.String("type")
		switch kind {
		case "http", "sse":
			config.Set("type", raw(kind))
			for _, key := range []string{"url", "headers", "oauth"} {
				if v := peer.Config.Get(key); v != nil {
					config.Set(key, v)
				}
			}
		case "claudeai-proxy":
			config.Set("type", raw(kind))
			for _, key := range []string{"url", "id"} {
				if v := peer.Config.Get(key); v != nil {
					config.Set(key, v)
				}
			}
		case "", "stdio":
			config.Set("type", raw("stdio"))
			for _, key := range []string{"command", "args"} {
				if v := peer.Config.Get(key); v != nil {
					config.Set(key, v)
				}
			}
		}
		if len(config.OwnProperties()) > 0 {
			value.Set("config", raw(config))
		}
		if peer.SDKManaged() && peer.Status == "failed" {
			value.Set("scope", raw("user"))
		} else if v := peer.Config.Get("scope"); v != nil {
			value.Set("scope", v)
		}
		if status == "connected" {
			tools := []protocol.JSONObject{}
			metadataTools := peer.Tools
			if peer.Config.String("type") == "sdk" && s.sdkStatusToolsHidden[peer.Name] {
				metadataTools = nil
			}
			for _, metadata := range metadataTools {
				annotations := object(metadata.Get("annotations"))
				flags := protocol.NewJSONObject()
				for _, key := range []struct{ source, target string }{{"readOnlyHint", "readOnly"}, {"destructiveHint", "destructive"}, {"openWorldHint", "openWorld"}} {
					if string(annotations.Get(key.source)) == "true" {
						flags.Set(key.target, raw(true))
					}
				}
				tools = append(tools, protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: metadata.Get("name")}, protocol.JSONField{Name: "annotations", Value: raw(flags)}))
			}
			value.Set("tools", raw(tools))
		}
		statuses = append(statuses, value)
	}
	if s.computerUse != nil {
		status, e := s.computerUseServerStatus(ctx, scope)
		if e != nil {
			return nil, e
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func (s *Service) Reconnect(ctx context.Context, scope *cooperative.Scope, name string) error {
	return s.ReconnectSDKControl(ctx, scope, name, true)
}

// Original reconnect updates dynamic/app state but retains sdkClients. Toggle
// enabling also replaces sdkClients, so a failed SDK snapshot retries next ASK.
func (s *Service) ReconnectSDKControl(ctx context.Context, scope *cooperative.Scope, name string, replaceSDK bool) error {
	if !scope.BelongsTo(s.options.Owner) {
		return cooperative.ErrRestricted
	}
	if name == ComputerUseServerName && s.computerUse != nil {
		_, e := s.computerUseServerStatus(ctx, scope)
		return e
	}
	previous := append([]mcpruntime.Connection{}, s.connections...)
	future, err := native(s, scope, func() (mcpruntime.Connection, error) {
		var config protocol.JSONObject
		current, err := s.options.Configurations.ByName(ctx, name)
		if err != nil {
			return mcpruntime.Connection{}, err
		}
		if current != nil {
			config = current.Clone()
		}
		if !config.IsObject() {
			for _, peer := range previous {
				if peer.Name == name {
					config = peer.Config.Clone()
					break
				}
			}
		}
		if !config.IsObject() {
			return mcpruntime.Connection{}, errors.New("Server not found: " + name)
		}
		if err := s.options.Runtime.Disconnect(ctx, name, config); err != nil {
			return mcpruntime.Connection{}, err
		}
		return s.options.Runtime.Connect(ctx, name, config)
	})
	if err != nil {
		return err
	}
	peer, err := future.Await(scope)
	if err != nil {
		return err
	}
	found := false
	for i, current := range s.connections {
		if current.Name != name {
			continue
		}
		if current.Config.String("type") == "sdk" {
			if s.sdkControlClients == nil {
				s.sdkControlClients = map[string]mcpruntime.Connection{}
			}
			if _, exists := s.sdkControlClients[name]; !exists {
				s.sdkControlOrder = append(s.sdkControlOrder, name)
			}
			s.sdkControlClients[name] = peer
			for i, current := range s.sdkControlAppClients {
				if current.Name == name {
					s.sdkControlAppClients[i] = peer
				}
			}
			if !replaceSDK {
				if s.sdkStatusToolsHidden == nil {
					s.sdkStatusToolsHidden = map[string]bool{}
				}
				s.sdkStatusToolsHidden[name] = true
			} else {
				s.connections[i] = peer
			}
		} else {
			s.connections[i] = peer
		}

		found = true
	}
	if !found {
		s.connections = append(s.connections, peer)
	}

	if peer.Status != "connected" && peer.Status != "disabled" {
		message := "Server status: " + peer.Status
		if peer.Status == "failed" {
			message = "Connection failed"
			if peer.Failure != nil {
				message = peer.Failure.Error()
			}
		}
		return errors.New(message)
	}
	return s.prefetchSkillResources(scope)
}

func sameConnection(a, b mcpruntime.Connection) bool {
	if a.Name != b.Name {
		return false
	}
	return mcpconfig.Hash(a.Config) == mcpconfig.Hash(b.Config)
}
func (s *Service) FailureCount(scope *cooperative.Scope) (int, error) {
	if !scope.BelongsTo(s.options.Owner) {
		return 0, cooperative.ErrRestricted
	}
	return len(s.failures), nil
}
