package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcptools"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainPrint) newNativeMCP() error {
	if err := m.initializeNativeSDKMCPState(); err != nil {
		return err
	}
	n := m.options.Native
	platform := m.options.Platform
	if platform == "windows" {
		platform = "win32"
	}
	plugins, _, err := m.nativeSettings.PluginMCP(m.pluginOptions, m.secure)
	if err != nil {
		return err
	}
	m.mcpConfigurations, err = m.nativeSettings.MCPConfigurations(plugins, true)
	if err != nil {
		return err
	}
	if err = m.initializeNativeCLIMCP(); err != nil {
		return err
	}
	if err = m.initializeNativeChrome(); err != nil {
		return err
	}
	var parent *MainPrint
	if d := m.options.DetachedAgent; d != nil {
		parent = d.authParent
		if d.preset != nil {
			parent = d.preset.parent
		}
	}
	if parent != nil && parent.claudeAIMCP != nil {
		m.claudeAIMCP, m.claudeAIMCPBorrowed = parent.claudeAIMCP, true
	} else {
		m.claudeAIMCP, err = m.newNativeClaudeAIMCP()
		if err != nil {
			return err
		}
	}
	auth, err := m.newNativeMCPAuth(platform)
	if err != nil {
		return err
	}
	cache, err := newNativeMCPAuthenticationCache(n)
	if err != nil {
		return err
	}
	m.mcpRuntime, err = mcpruntime.New(mcpruntime.Options{AuthenticationCache: cache, HasDiscoveryWithoutToken: auth.HasDiscoveryWithoutToken, ClearDiscoveryWithoutToken: auth.ClearDiscoveryWithoutToken, EnvironmentValue: m.nativeMCPEnvironmentValue, HeadersPolicy: m.nativeMCPHeadersPolicy, SessionIngressToken: m.nativeMCPIngressToken, PID: m.options.PID, SDK: m.nativeSDKMCPBindings, PrepareHTTP: m.claudeAIMCP.PrepareHTTP, CWD: n.CWD, Version: m.options.Version, Environment: n.Environment, Enabled: m.nativeSettings.MCPExecutionEnabled, Authenticate: auth.Authenticate, AuthenticateFlow: auth.AuthenticateWithOptions, HasOAuthTokens: auth.HasTokens, RevokeOAuth: auth.Revoke, ClearOAuth: auth.Clear, Auth: func(ctx context.Context, name string, config protocol.JSONObject) (mcpclient.AuthProvider, error) {
		if config.String("type") != "http" && config.String("type") != "sse" {
			return nil, nil
		}
		return auth.Provider(ctx, name, config)
	}})
	if err == nil {
		if parent != nil && parent.mcpRuntime != nil {
			candidate := m.mcpRuntime
			shared, e := parent.mcpRuntime.ShareNamespace(candidate)
			if e != nil {
				_ = candidate.Close(context.Background())
				return e
			}
			if shared != candidate {
				_ = candidate.Close(context.Background())
			}
			m.mcpRuntime, m.mcpBorrowed = shared, true
		}
	}
	return err
}
func (m *MainPrint) initializeMCPTools(scope *cooperative.Scope, binding *ownerasync.NativeBinding) error {
	if m.mcpRuntime == nil {
		return nil
	}
	var changed func(*cooperative.Scope) error
	if m.options.Interactive {
		changed = m.publishNativeMCPCatalog
	}
	service, err := mcptools.New(mcptools.Options{OnMCPDebug: func(scope *cooperative.Scope, name, message string) error {
		return m.logNativeMCPOnOwner(scope, name, message, false)
	}, OnMCPError: func(scope *cooperative.Scope, name, message string) error {
		return m.logNativeMCPOnOwner(scope, name, message, true)
	}, OnAuthenticationChanged: m.publishNativeMCPCatalog, OnCatalogChanged: changed, ResultOptions: m.nativeMCPResultOptions, ComputerUseEnabled: func(ctx context.Context) (bool, error) {
		return m.nativeSettings.MCPExecutionEnabled(ctx, "computer-use", protocol.NewJSONObject())
	}, All: m.nativeAllMCPConfigurations, SDKConfigurations: m.nativeSDKMCPConfigurations, Owner: m.Owner, NativeBinding: binding, Runtime: m.mcpRuntime, Configurations: m.mcpConfigurations, Environment: m.options.Native.Environment, PermissionContext: func(s *cooperative.Scope) (protocol.JSONObject, error) {
		_, permissions, err := m.Controller.SDKSettingsOnOwner(s)
		return permissions, err
	}, ScopeForContext: func(value any) *cooperative.Scope {
		if call, ok := value.(*builtinwrite.CallContext); ok && call != nil {
			return call.Scope
		}
		return nil
	}})
	if err != nil {
		return err
	}
	m.mcpTools = service
	if err = m.registerNativeComputerUse(scope, binding); err != nil {
		return err
	}
	if d := m.options.DetachedAgent; d != nil && d.preset != nil {
		err = service.InstallAgentConnections(scope, d.preset.inherited, nil, d.preset.resources)
	} else if d != nil && d.authParent != nil && d.authParent.mcpTools != nil {
		type inheritedMCP struct {
			peers     []mcpruntime.Connection
			resources *mcptools.AgentResourceSnapshot
		}
		snapshot, e := nativeAgentOwnerCall(m, scope, d.authParent, func(parentScope *cooperative.Scope) (inheritedMCP, error) {
			peers, e := d.authParent.mcpTools.AgentConnections(parentScope)
			if e != nil {
				return inheritedMCP{}, e
			}
			resources, e := d.authParent.mcpTools.AgentResources(parentScope)
			return inheritedMCP{peers, resources}, e
		})
		if e != nil {
			return e
		}
		err = service.InstallAgentConnections(scope, snapshot.peers, nil, snapshot.resources)
	} else if m.options.Interactive {
		_, err = service.BeginInteractiveDiscovery(scope, m.publishNativeMCPCatalog)
	} else {
		err = service.Discover(scope)
	}
	if err != nil {
		return err
	}
	m.mcpTools = service
	// runHeadless awaits its initial MCP discovery before constructing the
	// query host. Publish that completed prompt array here: interactive
	// callbacks run later, while this startup still has no Host to refresh.
	if !m.options.Interactive && m.Registry != nil {
		prompts, err := service.PromptCommands(scope)
		if err != nil {
			return err
		}
		m.Registry.ReplaceNativeMCPPrompts(prompts, true)
	}
	return m.bindNativeAgentAuthentication(scope, binding)
}

func (m *MainPrint) publishNativeMCPCatalog(scope *cooperative.Scope) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	if m.Host == nil || m.shuttingDown {
		return nil
	}
	definitions, err := m.mcpTools.PromptCommands(scope)
	if err != nil {
		return err
	}
	m.Registry.ReplaceNativeMCPPrompts(definitions, !m.options.Interactive)
	if err := m.Host.RefreshNativeToolCatalog(scope); err != nil {
		return err
	}
	if m.options.OnCommandsChanged != nil {
		return m.options.OnCommandsChanged(m.Registry.ControlCommands())
	}
	return nil
}
