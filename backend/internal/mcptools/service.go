// Package mcptools registers actual native MCP peers with the existing Harness.
package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/computerusemcp"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

type Options struct {
	ResultOptions           func(*cooperative.Scope) (mcpruntime.RichResultOptions, error)
	Owner                   *cooperative.Owner
	NativeBinding           *ownerasync.NativeBinding
	Runtime                 *mcpruntime.Service
	Configurations          *mcpconfig.Service
	All                     func(context.Context) (mcpconfig.Result, error)
	SDKConfigurations       func(context.Context) (protocol.JSONObject, error)
	Environment             map[string]string
	PermissionContext       func(*cooperative.Scope) (protocol.JSONObject, error)
	ScopeForContext         func(any) *cooperative.Scope
	ComputerUseEnabled      func(context.Context) (bool, error)
	OnCatalogChanged        func(*cooperative.Scope) error
	OnAuthenticationChanged func(*cooperative.Scope) error
	OnMCPDebug              func(*cooperative.Scope, string, string) error
	OnMCPError              func(*cooperative.Scope, string, string) error
}
type Service struct {
	authenticationTools   map[string]authenticationToolView
	authenticationJobs    *authenticationToolJobs
	agentAuthentication   *agentAuthenticationContext
	agentResources        *AgentResourceSnapshot
	options               Options
	computerUse           *computerusemcp.Binder
	computerUseConfig     protocol.JSONObject
	inactiveComputerUse   inactiveComputerUseCleanup
	connections           []mcpruntime.Connection
	dynamicConfigurations protocol.JSONObject
	sdkControlClients     map[string]mcpruntime.Connection
	sdkControlAppClients  []mcpruntime.Connection
	sdkControlOrder       []string
	sdkStatusToolsHidden  map[string]bool
	failures              []protocol.JSONObject
	skillResources        map[string]skillResourceCatalogue
	agentResourceSnapshot bool
	discoveryGeneration   uint64
	peerGeneration        map[string]uint64
	promptCommands        map[string]promptCatalogue
	toolNotifications     map[string]toolNotification
}

func New(o Options) (*Service, error) {
	if o.Owner == nil || o.NativeBinding == nil || !o.NativeBinding.Owns(o.Owner) || o.Runtime == nil || o.Configurations == nil || o.PermissionContext == nil || o.ScopeForContext == nil {
		return nil, errors.New("native MCP tools require actual shared owner, native binding, runtime, configuration and permission producers")
	}
	return &Service{options: o, authenticationJobs: &authenticationToolJobs{}}, nil
}
func raw(value any) json.RawMessage { out, _ := json.Marshal(value); return out }
func object(value json.RawMessage) protocol.JSONObject {
	var out protocol.JSONObject
	_ = out.UnmarshalJSON(value)
	return out
}
func native[T any](s *Service, scope *cooperative.Scope, work func() (T, error)) (*ownerasync.OwnerFuture[T], error) {
	if !scope.BelongsTo(s.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	ref, err := s.options.NativeBinding.Acquire(scope)
	if err != nil {
		return nil, err
	}
	future, err := ownerasync.NewOwnerFuture[T](s.options.Owner)
	if err != nil {
		ref.DeliveryFinished()
		return nil, err
	}
	go func() {
		value, failure := work()
		receipt, err := s.options.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
			defer ref.DeliveryFinished()
			if failure != nil {
				return future.Reject(next, failure)
			}
			return future.Resolve(next, value)
		})
		if err != nil {
			ref.DeliveryFinished()
			_ = future.RejectAdministrative(err)
		} else {
			ref.ReclaimRejectedReceipt(receipt)
		}
	}()
	return future, nil
}

type discovery struct {
	connections []mcpruntime.Connection
	failures    []protocol.JSONObject
	sdkChanged  bool
}

func (s *Service) Discover(scope *cooperative.Scope) error {
	if !scope.BelongsTo(s.options.Owner) {
		return cooperative.ErrRestricted
	}
	s.discoveryGeneration++
	previous := append([]mcpruntime.Connection{}, s.connections...)
	dynamicConfigurations := s.dynamicConfigurations.Clone()
	future, err := native(s, scope, func() (discovery, error) {
		if err := s.options.Runtime.BeginDiscoveryBatch(context.Background()); err != nil {
			return discovery{}, err
		}
		var configs mcpconfig.Result
		var err error
		if s.options.All != nil {
			configs, err = s.options.All(context.Background())
		} else {
			configs, err = s.options.Configurations.Configurations(context.Background(), protocol.NewJSONObject(), protocol.NewJSONObject())
		}
		if err != nil {
			return discovery{}, err
		}
		result := discovery{connections: []mcpruntime.Connection{}, failures: configs.Errors}
		sdkConfigs := protocol.NewJSONObject()
		sets := []protocol.JSONObject{configs.Servers, dynamicConfigurations}
		if s.options.SDKConfigurations != nil {
			sdkConfigs, err = s.options.SDKConfigurations(context.Background())
			if err != nil {
				return discovery{}, err
			}
			sets = append(sets, sdkConfigs)
		}
		sdkChanged, err := sdkNeedsRefresh(previous, sdkConfigs)
		if err != nil {
			return discovery{}, err
		}
		result.sdkChanged = sdkChanged
		if sdkChanged {
			for _, old := range previous {
				if old.Config.String("type") == "sdk" && sdkConfigs.Get(old.Name) == nil {
					if err = s.options.Runtime.CloseConnection(context.Background(), old); err != nil {
						return discovery{}, err
					}
				}
			}
		}
		for setIndex, set := range sets {
			if setIndex == len(sets)-1 && s.options.SDKConfigurations != nil && sdkChanged {
				peers, err := s.connectSDKAttempts(context.Background(), set)
				if err != nil {
					return discovery{}, err
				}
				result.connections = append(result.connections, peers...)
				continue
			}
			for _, p := range set.OwnProperties() {
				var name string
				_ = json.Unmarshal(p.NameJSON, &name)
				config := object(p.Value)
				if config.String("type") == "sdk" {
					enabled, err := s.options.Runtime.ExecutionEnabled(context.Background(), name, config)
					if err != nil {
						return discovery{}, err
					}
					if !enabled {
						continue
					}
					config.Set("scope", raw("dynamic"))
				}
				if mcpclient.NormalizeName(name) == ComputerUseServerName && (config.String("type") == "" || config.String("type") == "stdio") {
					if s.computerUse == nil {
						return discovery{}, errors.New("native Computer Use in-process SDK binding is unavailable")
					}
					continue
				}
				for _, old := range previous {
					if sameConnection(old, mcpruntime.Connection{Name: name, Config: config}) {
						config = old.Config
						break
					}
				}
				var peer mcpruntime.Connection
				if config.String("type") == "sdk" && !sdkChanged {
					for _, old := range previous {
						if old.Config.String("type") == "sdk" && old.Name == name {
							peer = old
							break
						}
					}
				} else if config.String("type") == "sdk" && sdkChanged {
					peer, err = s.options.Runtime.ConnectSDKAttempt(context.Background(), name, config)
				} else {
					peer, err = s.options.Runtime.ConnectForDiscovery(context.Background(), name, config, true)
				}
				if err != nil {
					return discovery{}, err
				}
				result.connections = append(result.connections, peer)
			}
		}
		for _, old := range previous {
			// Source preserves temporarily absent manual clients. Only dynamic
			// plugin removal or an actual config-content change retires a peer.
			if old.Config.String("type") != "sdk" && configs.Servers.Get(old.Name) == nil && old.Config.String("scope") != "dynamic" {
				result.connections = append(result.connections, old)
				continue
			}
			retained := false
			for _, current := range result.connections {
				if sameConnection(old, current) {
					retained = true
					break
				}
			}
			if !retained && old.Config.String("type") != "sdk" {
				if err = s.options.Runtime.Disconnect(context.Background(), old.Name, old.Config); err != nil {
					return discovery{}, err
				}
			}
		}
		return result, nil
	})
	if err != nil {
		return err
	}
	result, err := future.Await(scope)
	if err == nil {
		s.connections = result.connections
		for _, peer := range s.connections {
			s.publishAuthenticationTool(peer)
		}
		s.failures = result.failures
		// updateSdkMcp re-publishes the shared SDK tools after its changed-name pass.
		if result.sdkChanged {
			for _, peer := range result.connections {
				if peer.Config.String("type") == "sdk" {
					delete(s.sdkStatusToolsHidden, peer.Name)
				}
			}
		}
		prompts, err := s.beginPromptCommands(scope, s.connections)
		if err != nil {
			return err
		}
		if err := s.prefetchSkillResources(scope); err != nil {
			return err
		}
		if err := s.finishPromptCommands(scope, prompts); err != nil {
			return err
		}
		for _, peer := range s.connections {
			if err := s.installToolNotifications(scope, peer); err != nil {
				return err
			}
		}
		return nil
	}
	return err
}
func (s *Service) Status(scope *cooperative.Scope) ([]protocol.JSONObject, error) {
	if !scope.BelongsTo(s.options.Owner) {
		return nil, cooperative.ErrRestricted
	}
	out := []protocol.JSONObject{}
	for _, peer := range s.connections {
		out = append(out, protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: raw(peer.Name)}, protocol.JSONField{Name: "status", Value: raw(peer.Status)}))
	}
	if s.computerUse != nil {
		future, e := native(s, scope, func() (bool, error) { return s.options.ComputerUseEnabled(context.Background()) })
		if e != nil {
			return nil, e
		}
		enabled, e := future.Await(scope)
		if e != nil {
			return nil, e
		}
		status := "connected"
		if !enabled {
			status = "disabled"
		}
		out = append(out, protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: raw(ComputerUseServerName)}, protocol.JSONField{Name: "status", Value: raw(status)}))
	}
	return out, nil
}
func ruleMatches(rule, qualified, server string) bool {
	return rule == qualified || rule == qualified+"(*)" || rule == "mcp__"+mcpclient.NormalizeName(server) || rule == "mcp__"+mcpclient.NormalizeName(server)+"__*"
}
func (s *Service) Permission(scope *cooperative.Scope, name string, input *protocol.JSONObject) (queryloop.ToolPermission, error) {
	ctx, err := s.options.PermissionContext(scope)
	decision := queryloop.ToolPermission{Input: input, Behavior: "ask", Message: raw("MCPTool requires permission.")}
	if err != nil {
		return decision, err
	}
	server := ""
	for _, peer := range s.connections {
		if s.hasAuthenticationTool(peer) && mcpclient.ToolName(peer.Name, "authenticate") == name {
			server = peer.Name
		}
	}
	for _, peer := range s.connections {
		for _, tool := range peer.Tools {
			if mcpclient.ToolName(peer.Name, tool.String("name")) == name {
				server = peer.Name
			}
		}
	}
	if s.computerUse != nil && (name == mcpclient.ToolName(ComputerUseServerName, "js") || name == mcpclient.ToolName(ComputerUseServerName, "js_reset")) {
		server = ComputerUseServerName
	}
	for _, key := range []string{"alwaysDenyRules", "alwaysAskRules", "alwaysAllowRules"} {
		var sources protocol.JSONObject
		_ = sources.UnmarshalJSON(ctx.Get(key))
		for _, source := range sources.OwnProperties() {
			var rules []string
			_ = json.Unmarshal(source.Value, &rules)
			for _, rule := range rules {
				if ruleMatches(rule, name, server) {
					if key == "alwaysDenyRules" {
						decision.Behavior = "deny"
						decision.Message = raw("Permission to use " + name + " has been denied.")
						return decision, nil
					}
					if key == "alwaysAskRules" && ctx.String("mode") != "bypassPermissions" {
						return decision, nil
					}
					if key == "alwaysAllowRules" {
						decision.Behavior = "allow"
						decision.Message = nil
						return decision, nil
					}
				}
			}
		}
	}
	authentication := false
	for _, peer := range s.connections {
		authentication = authentication || s.hasAuthenticationTool(peer) && mcpclient.ToolName(peer.Name, "authenticate") == name
	}
	if ctx.String("mode") == "bypassPermissions" || authentication {
		decision.Behavior = "allow"
		decision.Message = nil
	}
	return decision, nil
}
func descriptionLimit(env map[string]string) int {
	value := strings.TrimSpace(env["CLAUDE_CODE_MAX_MCP_DESCRIPTION_LENGTH"])
	if value == "" {
		return 2048
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return 2048
		}
	}
	n, err := strconv.ParseUint(value, 10, 53)
	if err != nil || n == 0 {
		return 2048
	}
	if n > uint64(^uint(0)>>1) {
		return 2048
	}
	return int(n)
}
func (s *Service) Catalog(scope *cooperative.Scope) ([]toolschema.Definition, []queryloop.RegisteredTool, error) {
	return s.catalog(scope, true)
}

// PermissionPromptCatalog reflects Source appState.mcp.tools before model-only deny filtering.
func (s *Service) PermissionPromptCatalog(scope *cooperative.Scope) ([]toolschema.Definition, []queryloop.RegisteredTool, error) {
	return s.catalog(scope, false)
}
func (s *Service) catalog(scope *cooperative.Scope, filterDenied bool) ([]toolschema.Definition, []queryloop.RegisteredTool, error) {
	if !scope.BelongsTo(s.options.Owner) {
		return nil, nil, cooperative.ErrRestricted
	}
	defs := []toolschema.Definition{}
	registered := []queryloop.RegisteredTool{}
	seen := map[string]bool{}
	permissions, err := s.options.PermissionContext(scope)
	if err != nil {
		return nil, nil, err
	}
	for _, peer := range s.connections {
		authentication := s.hasAuthenticationTool(peer)
		if peer.Status != "connected" && !authentication {
			continue
		}
		metadataTools := peer.Tools
		if authentication {
			metadataTools = []protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: raw("authenticate")})}
		}
		for _, metadata := range metadataTools {
			name := mcpclient.ToolName(peer.Name, metadata.String("name"))
			if seen[name] {
				continue
			}
			seen[name] = true
			denied := false
			var deny protocol.JSONObject
			_ = deny.UnmarshalJSON(permissions.Get("alwaysDenyRules"))
			for _, source := range deny.OwnProperties() {
				var rules []string
				_ = json.Unmarshal(source.Value, &rules)
				for _, rule := range rules {
					denied = denied || ruleMatches(rule, name, peer.Name)
				}
			}
			if filterDenied && denied {
				continue
			}
			if authentication {
				definition, tool, err := s.authenticationTool(peer)
				if err != nil {
					return nil, nil, err
				}
				defs = append(defs, definition)
				registered = append(registered, tool)
				continue
			}
			prompt := metadata.Get("description")
			if prompt == nil {
				prompt = raw("")
			}
			prompt = mcpclient.TruncateDescription(prompt, descriptionLimit(s.options.Environment))
			alwaysLoad := string(object(metadata.Get("_meta")).Get("anthropic/alwaysLoad")) == "true"
			definition := toolschema.Definition{IsMCP: true, AlwaysLoad: alwaysLoad, Kind: "registered", Name: name, InputSchema: &toolschema.Schema{Kind: "object", UnknownKeys: "passthrough"}, InputJSONSchema: metadata.Get("inputSchema"), Description: metadata.Get("description"), Prompt: prompt}
			readOnly := string(object(metadata.Get("annotations")).Get("readOnlyHint")) == "true"
			tool := queryloop.RegisteredTool{Name: name, IsMCP: true, AlwaysLoad: alwaysLoad, PersistenceThreshold: 50000, ParseInput: func(input protocol.JSONObject) (protocol.JSONObject, error) { return input.Clone(), nil }, IsConcurrencySafe: func(protocol.JSONObject) bool { return readOnly }, MapResultJSON: func(data, id json.RawMessage) (protocol.JSONObject, error) {
				return protocol.NewJSONObject(protocol.JSONField{Name: "tool_use_id", Value: id}, protocol.JSONField{Name: "type", Value: raw("tool_result")}, protocol.JSONField{Name: "content", Value: data}), nil
			}}
			start := func(ctx context.Context, input protocol.JSONObject, call queryloop.JSONToolCallContext) (*ownerasync.OwnerFuture[queryloop.ToolCallResult], error) {
				scope := s.options.ScopeForContext(call.Opaque)
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				var resultOptions mcpruntime.RichResultOptions
				richBound := s.options.ResultOptions != nil
				releaseResult := func() {}
				if richBound {
					var err error
					resultOptions, err = s.options.ResultOptions(scope)
					if err != nil {
						return nil, err
					}
					ref, err := s.options.NativeBinding.Acquire(scope)
					if err != nil {
						return nil, err
					}
					releaseResult = func() {
						receipt, err := s.options.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error { defer ref.DeliveryFinished(); return nil })
						if err != nil {
							ref.DeliveryFinished()
						} else {
							ref.ReclaimRejectedReceipt(receipt)
						}
					}
					resultOptions.OnSettled = releaseResult
				}
				meta := protocol.NewJSONObject()
				if len(call.ToolUseID) > 0 && string(call.ToolUseID) != `""` {
					meta.Set("claudecode/toolUseId", call.ToolUseID)
				}
				transformed := false
				future, startError := native(s, scope, func() (queryloop.ToolCallResult, error) {
					defer func() {
						if !transformed {
							releaseResult()
						}
					}()
					for attempt := 0; ; attempt++ {
						current, err := s.options.Runtime.RefreshConnection(ctx, peer.ExecutionConnection())
						if err != nil {
							return queryloop.ToolCallResult{}, err
						}
						if current.Status == "disabled" {
							return queryloop.ToolCallResult{}, errors.New("MCP server \"" + peer.Name + "\" is disabled")
						}
						if current.Status != "connected" {
							return queryloop.ToolCallResult{}, errors.New("MCP server \"" + peer.Name + "\" is not connected")
						}
						// callMCPTool's external timeout can win before the SDK's
						// own timeout/abort finishes. Retain its actual native work
						// independently of the earlier model tool-result delivery.
						var requestRef *ownerasync.NativeReference
						receipt, acquireError := s.options.Owner.SubmitIndependent(ctx, cooperative.Read, func(next *cooperative.Scope) error {
							var err error
							requestRef, err = s.options.NativeBinding.Acquire(next)
							return err
						})
						if acquireError != nil {
							return queryloop.ToolCallResult{}, acquireError
						}
						if err := receipt.Wait(); err != nil {
							return queryloop.ToolCallResult{}, err
						}
						result, err := current.Client.CallToolSource(ctx, metadata.Get("name"), input, meta, requestRef.DeliveryFinished)
						if err != nil {
							if attempt < 1 && mcpclient.IsSessionExpired(err) {
								if err = s.options.Runtime.DisconnectConnection(ctx, current); err != nil {
									return queryloop.ToolCallResult{}, err
								}
								continue
							}
							var auth *mcpclient.AuthError
							if errors.As(err, &auth) {
								return queryloop.ToolCallResult{}, errors.New("MCP server \"" + peer.Name + "\" requires re-authorization (token expired)")
							}
							return queryloop.ToolCallResult{}, err
						}
						if string(result.Get("isError")) == "true" {
							detail := "Unknown error"
							var content []protocol.JSONObject
							_ = json.Unmarshal(result.Get("content"), &content)
							if len(content) > 0 && content[0].Get("text") != nil {
								detail = content[0].String("text")
							}
							return queryloop.ToolCallResult{}, errors.New(detail)
						}
						var data json.RawMessage
						if richBound {
							transformed = true
							data, err = mcpruntime.TransformRichResult(ctx, result, metadata.String("name"), peer.Name, resultOptions)
						} else {
							data, err = mcpruntime.TransformResult(result, metadata.String("name"), peer.Name)
						}
						if err != nil {
							return queryloop.ToolCallResult{}, err
						}
						out := queryloop.ToolCallResult{Data: data}
						if result.Get("_meta") != nil || result.Get("structuredContent") != nil {
							mcpMeta := protocol.NewJSONObject()
							if result.Get("_meta") != nil {
								mcpMeta.Set("_meta", result.Get("_meta"))
							}
							if result.Get("structuredContent") != nil {
								mcpMeta.Set("structuredContent", result.Get("structuredContent"))
							}
							out.MCPMeta = raw(mcpMeta)
						}
						return out, nil
					}
				})
				if startError != nil {
					releaseResult()
				}
				return future, startError
			}
			tool.StartCallJSON = start
			tool.CallJSON = func(ctx context.Context, input protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
				future, err := start(ctx, input, call)
				if err != nil {
					return queryloop.ToolCallResult{}, err
				}
				return future.Await(s.options.ScopeForContext(call.Opaque))
			}
			tool, err = queryloop.BindNativeMCP(tool, s.options.Runtime)
			if err != nil {
				return nil, nil, err
			}
			defs = append(defs, definition)
			registered = append(registered, tool)
		}
	}
	extra, internal, e := s.computerUseCatalog(scope)
	if e != nil {
		return nil, nil, e
	}
	defs = append(defs, extra...)
	registered = append(registered, internal...)
	return defs, registered, nil
}
