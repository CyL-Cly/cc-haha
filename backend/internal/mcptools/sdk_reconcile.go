package mcptools

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type SDKSetServersResult struct {
	Configurations protocol.JSONObject
	Response       protocol.JSONObject
	SDKChanged     bool
}

func propertyName(field protocol.JSONProperty) string {
	var name string
	_ = json.Unmarshal(field.NameJSON, &name)
	return name
}

// ReconcileSDKServers publishes pending clients before the public ACK. Only
// removed current clients are cleaned up; same-name configs and old Tool peers
// retain Source's exact original identity.
func (s *Service) ReconcileSDKServers(scope *cooperative.Scope, current, servers protocol.JSONObject) (SDKSetServersResult, error) {
	if !scope.BelongsTo(s.options.Owner) {
		return SDKSetServersResult{}, cooperative.ErrRestricted
	}
	previous := append([]mcpruntime.Connection{}, s.connections...)
	dynamic := s.dynamicConfigurations.Clone()
	type nativeReconciliation struct {
		result  SDKSetServersResult
		process protocol.JSONObject
		peers   []mcpruntime.Connection
	}
	future, err := native(s, scope, func() (nativeReconciliation, error) {
		allowed, blocked, err := s.options.Configurations.FilterProcessInjection(context.Background(), servers)
		if err != nil {
			return nativeReconciliation{}, err
		}
		sdk, process := protocol.NewJSONObject(), protocol.NewJSONObject()
		for _, field := range allowed.OwnProperties() {
			target := &process
			if object(field.Value).String("type") == "sdk" {
				target = &sdk
			}
			_ = target.SetPropertyJSON(field.NameJSON, field.Value)
		}
		next := current.Clone()
		added, removed := []string{}, []string{}
		failures := protocol.NewJSONObject()
		for _, name := range blocked {
			failures.Set(name, raw("Blocked by enterprise policy (allowedMcpServers/deniedMcpServers)"))
		}
		for _, field := range current.OwnProperties() {
			name := propertyName(field)
			if sdk.Get(name) != nil {
				continue
			}
			for _, peer := range previous {
				if peer.Config.String("type") == "sdk" && peer.Name == name && peer.Status == "connected" {
					if err = s.options.Runtime.CloseConnection(context.Background(), peer); err != nil {
						return nativeReconciliation{}, err
					}
					break
				}
			}
			next.Delete(name)
			removed = append(removed, name)
		}
		for _, field := range sdk.OwnProperties() {
			name := propertyName(field)
			if current.Get(name) == nil {
				_ = next.SetPropertyJSON(field.NameJSON, field.Value)
				added = append(added, name)
			}
		}
		changed := len(added) > 0 || len(removed) > 0
		// Reconcile actual process clients in the original removal/addition order.
		replacements := map[string]bool{}
		for _, field := range dynamic.OwnProperties() {
			name := propertyName(field)
			wanted := process.Get(name)
			if wanted == nil || mcpconfig.Hash(object(field.Value)) != mcpconfig.Hash(object(wanted)) {
				replacements[name] = wanted != nil
				for _, peer := range previous {
					if peer.Name == name && peer.Config.String("type") != "sdk" {
						_ = s.options.Runtime.Disconnect(context.Background(), name, peer.Config)
						break
					}
				}
				if wanted == nil {
					removed = append(removed, name)
				}
			}
		}
		processPeers := []mcpruntime.Connection{}
		scoped := protocol.NewJSONObject()
		for _, field := range process.OwnProperties() {
			name := propertyName(field)
			config := object(field.Value)
			config.Set("scope", raw("dynamic"))
			_ = scoped.SetPropertyJSON(field.NameJSON, raw(config))
			if dynamic.Get(name) == nil || replacements[name] {
				peer, e := s.options.Runtime.Connect(context.Background(), name, config)
				if e != nil {
					failures.Set(name, raw(e.Error()))
					continue
				}
				processPeers = append(processPeers, peer)
				added = append(added, name)
				if peer.Status == "failed" {
					message := "Connection failed"
					if peer.Failure != nil {
						message = peer.Failure.Error()
					}
					failures.Set(name, raw(message))
				}
			} else {
				for _, peer := range previous {
					if peer.Name == name && peer.Config.String("type") != "sdk" {
						processPeers = append(processPeers, peer)
						break
					}
				}
			}
		}
		// The owner installs these snapshots after native completion; no native
		// goroutine writes the mutable catalogue.
		result := SDKSetServersResult{Configurations: next, Response: protocol.NewJSONObject(protocol.JSONField{Name: "added", Value: raw(added)}, protocol.JSONField{Name: "removed", Value: raw(removed)}, protocol.JSONField{Name: "errors", Value: raw(failures)}), SDKChanged: changed}
		return nativeReconciliation{result: result, process: scoped, peers: processPeers}, nil
	})
	if err != nil {
		return SDKSetServersResult{}, err
	}
	completed, err := future.Await(scope)
	result := completed.result
	if err != nil {
		return result, err
	}
	// Installation is separate from the physical work so SDK input responses
	// keep advancing through the same Owner while peers connect.
	process := completed.process
	installed := []mcpruntime.Connection{}
	for _, peer := range previous {
		if peer.Config.String("type") == "sdk" {
			if result.Configurations.Get(peer.Name) != nil {
				installed = append(installed, peer)
			}
		} else if dynamic.Get(peer.Name) == nil && process.Get(peer.Name) == nil {
			installed = append(installed, peer)
		}
	}
	for _, field := range result.Configurations.OwnProperties() {
		name := propertyName(field)
		if current.Get(name) == nil {
			config := object(field.Value)
			config.Set("scope", raw("dynamic"))
			pending, err := s.options.Runtime.PendingSDK(name, config)
			if err != nil {
				return result, err
			}
			installed = append(installed, pending)
		}
	}
	installed = append(installed, completed.peers...)
	s.connections, s.dynamicConfigurations = installed, process
	appClients := []mcpruntime.Connection{}
	for _, peer := range s.sdkControlAppClients {
		if dynamic.Get(peer.Name) == nil && process.Get(peer.Name) == nil {
			appClients = append(appClients, peer)
		}
	}
	for _, name := range s.sdkControlOrder {
		if peer, ok := s.sdkControlClients[name]; ok {
			appClients = append(appClients, peer)
		}
	}
	s.sdkControlAppClients = appClients
	return result, s.prefetchSkillResources(scope)
}

func (s *Service) DisableServer(scope *cooperative.Scope, name string) error {
	if !scope.BelongsTo(s.options.Owner) {
		return cooperative.ErrRestricted
	}
	peers := append([]mcpruntime.Connection{}, s.connections...)
	for i, peer := range s.connections {
		if peer.Name == name {
			peer.Status, peer.Client, peer.Tools = "disabled", nil, []protocol.JSONObject{}
			s.connections[i] = peer
			if s.sdkControlClients != nil {
				s.sdkControlClients[name] = peer
				for i, current := range s.sdkControlAppClients {
					if current.Name == name {
						s.sdkControlAppClients[i] = peer
					}
				}
			}
		}
	}
	future, err := native(s, scope, func() (struct{}, error) {
		for _, peer := range peers {
			if peer.Name == name {
				if peer.Config.String("type") != "sdk" {
					_ = s.options.Runtime.Disconnect(context.Background(), name, peer.Config)
				} else if peer.Status == "connected" {
					if err := s.options.Runtime.CloseConnection(context.Background(), peer); err != nil {
						return struct{}{}, err
					}
				}
			}
		}
		return struct{}{}, nil
	})
	if err != nil {
		return err
	}
	_, err = future.Await(scope)
	return err
}

func (s *Service) ServerConfiguration(scope *cooperative.Scope, name string) (*protocol.JSONObject, error) {
	if !scope.BelongsTo(s.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	future, err := native(s, scope, func() (*protocol.JSONObject, error) {
		return s.options.Configurations.ByName(context.Background(), name)
	})
	if err != nil {
		return nil, err
	}
	config, err := future.Await(scope)
	if err != nil || config != nil {
		return config, err
	}
	for _, peer := range s.connections {
		if peer.Name == name {
			copy := peer.Config.Clone()
			return &copy, nil
		}
	}
	if peer, ok := s.sdkControlClients[name]; ok {
		copy := peer.Config.Clone()
		return &copy, nil
	}
	return nil, nil
}
