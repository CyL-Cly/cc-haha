package mcptools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type interactiveConfigurations struct {
	servers  protocol.JSONObject
	failures []protocol.JSONObject
}

// BeginInteractiveDiscovery mirrors main's prefetch/useManageMCPConnections:
// rendering and the first query see the current catalog, then each server's
// completed connection publishes on the same Owner. A slow peer cannot hold
// either rendering or a faster peer's tools. Headless Discover still awaits.
func (s *Service) BeginInteractiveDiscovery(scope *cooperative.Scope, onChanged func(*cooperative.Scope) error) (*ownerasync.OwnerFuture[struct{}], error) {
	if !scope.BelongsTo(s.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	s.discoveryGeneration++
	generation := s.discoveryGeneration
	dynamic := s.dynamicConfigurations.Clone()
	read, err := native(s, scope, func() (interactiveConfigurations, error) {
		if err := s.options.Runtime.BeginDiscoveryBatch(context.Background()); err != nil {
			return interactiveConfigurations{}, err
		}
		var configs mcpconfig.Result
		var err error
		if s.options.All != nil {
			configs, err = s.options.All(context.Background())
		} else {
			configs, err = s.options.Configurations.Configurations(context.Background(), protocol.NewJSONObject(), protocol.NewJSONObject())
		}
		if err != nil {
			return interactiveConfigurations{}, err
		}
		merged := configs.Servers.Clone()
		for _, field := range dynamic.OwnProperties() {
			var name string
			_ = json.Unmarshal(field.NameJSON, &name)
			merged.Set(name, field.Value)
		}
		if s.options.SDKConfigurations != nil {
			sdk, err := s.options.SDKConfigurations(context.Background())
			if err != nil {
				return interactiveConfigurations{}, err
			}
			for _, field := range sdk.OwnProperties() {
				var name string
				_ = json.Unmarshal(field.NameJSON, &name)
				merged.Set(name, field.Value)
			}
		}
		return interactiveConfigurations{merged, configs.Errors}, nil
	})
	if err != nil {
		return nil, err
	}
	return read.Then(scope, func(next *cooperative.Scope, configs interactiveConfigurations, failure error) error {
		if failure != nil {
			return failure
		}
		if generation != s.discoveryGeneration {
			return nil
		}
		s.failures = configs.failures
		localLimit, err := interactiveConcurrency(s.options.Environment["MCP_SERVER_CONNECTION_BATCH_SIZE"], 3)
		if err != nil {
			return err
		}
		remoteLimit, err := interactiveConcurrency(s.options.Environment["MCP_REMOTE_SERVER_CONNECTION_BATCH_SIZE"], 20)
		if err != nil {
			return err
		}
		// pMap creates at most one worker per item even for a very large
		// configured ceiling. Keep the native semaphore bounded likewise.
		items := max(1, len(configs.servers.OwnProperties()))
		local, remote := make(chan struct{}, min(items, localLimit)), make(chan struct{}, min(items, remoteLimit))
		for _, field := range configs.servers.OwnProperties() {
			var name string
			_ = json.Unmarshal(field.NameJSON, &name)
			config := object(field.Value)
			if mcpclient.NormalizeName(name) == ComputerUseServerName && (config.String("type") == "" || config.String("type") == "stdio") {
				continue
			}
			index := len(s.connections)
			s.connections = append(s.connections, mcpruntime.Connection{Name: name, Config: config.Clone(), Status: "pending", Tools: []protocol.JSONObject{}})
			slots := remote
			if config.String("type") == "" || config.String("type") == "stdio" || config.String("type") == "sdk" {
				slots = local
			}
			if err := s.beginInteractivePeer(next, generation, index, name, config, slots, onChanged); err != nil {
				return err
			}
		}
		if onChanged != nil {
			return onChanged(next)
		}
		return nil
	})
}

type interactivePeer struct {
	connection         mcpruntime.Connection
	prompts, resources []protocol.JSONObject
}

func interactiveConcurrency(value string, fallback int) (int, error) {
	value = strings.TrimSpace(value)
	end := 0
	if len(value) > 0 && (value[0] == '+' || value[0] == '-') {
		end++
	}
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	count, err := strconv.Atoi(value[:end])
	if err != nil || count == 0 {
		return fallback, nil
	}
	if count < 0 {
		return 0, fmt.Errorf("Expected concurrency to be an integer from 1 and up, got %d", count)
	}
	return count, nil
}

func (s *Service) beginInteractivePeer(scope *cooperative.Scope, generation uint64, index int, name string, config protocol.JSONObject, slots chan struct{}, onChanged func(*cooperative.Scope) error) error {
	peerGeneration := s.peerGeneration[name]
	// The original fetchResourcesForClient memo survives /clear. Capture its
	// current immutable rows on Owner before the physical worker runs.
	resourceCache := s.skillResources[name]
	resourceCache.rows = cloneResourceRows(resourceCache.rows)
	read, err := native(s, scope, func() (interactivePeer, error) {
		slots <- struct{}{}
		defer func() { <-slots }()
		var peer mcpruntime.Connection
		var err error
		if config.String("type") == "sdk" {
			peer, err = s.options.Runtime.ConnectSDKAttempt(context.Background(), name, config)
		} else {
			peer, err = s.options.Runtime.ConnectForDiscovery(context.Background(), name, config, false)
		}
		if err != nil {
			return interactivePeer{}, err
		}
		result := interactivePeer{connection: peer}
		if peer.Status != "connected" || peer.Client == nil {
			return result, nil
		}
		// Source starts tools, prompts and resources together per server. The
		// native reference and pool slot retain every physical sibling until
		// settlement; each completed peer publishes independently.
		var wg sync.WaitGroup
		var toolsError, promptsError error
		wg.Add(3)
		go func() {
			defer wg.Done()
			result.connection, toolsError = s.options.Runtime.RefreshConnection(context.Background(), peer)
		}()
		go func() {
			defer wg.Done()
			if config.String("type") != "sdk" {
				result.prompts, promptsError = s.options.Runtime.FetchPrompts(context.Background(), peer)
			}
		}()
		go func() {
			defer wg.Done()
			if resourceCache.fresh && resourceCache.client == peer.Client {
				result.resources = resourceCache.rows
			} else {
				result.resources = fetchSkillResources(context.Background(), peer)
			}
		}()
		wg.Wait()
		if toolsError != nil {
			return result, toolsError
		}
		return result, promptsError
	})
	if err != nil {
		return err
	}
	_, err = read.Then(scope, func(next *cooperative.Scope, result interactivePeer, failure error) error {
		if generation != s.discoveryGeneration || peerGeneration != s.peerGeneration[name] {
			return nil
		}
		peer := result.connection
		if failure != nil {
			peer = mcpruntime.Connection{Name: name, Config: config, Status: "failed", Tools: []protocol.JSONObject{}, Failure: failure}
		}
		if index >= len(s.connections) || s.connections[index].Name != name {
			return cooperative.ErrRestricted
		}
		s.connections[index] = peer
		s.publishAuthenticationTool(peer)
		if peer.Status == "connected" && peer.Client != nil {
			if err := s.installSkillResources(next, peer, result.resources); err != nil {
				return err
			}
			if err := s.installPromptCommands(next, peer, result.prompts); err != nil {
				return err
			}
			if err := s.installToolNotifications(next, peer); err != nil {
				return err
			}
		}
		if onChanged != nil {
			return onChanged(next)
		}
		return nil
	})
	return err
}
