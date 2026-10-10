package application

import (
	"context"
	"errors"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/connectors"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
)

// Connector state is loaded only after route validation. A single lifecycle
// owner prevents initialization racing with server shutdown.
type desktopConnectorRuntime struct {
	mu                sync.Mutex
	native            *clibootstrap.NativeBoundary
	settings          *clibootstrap.NativeProcessSettings
	editable          *settings.Service
	security          func(context.Context, securestorage.Command) ([]byte, error)
	reload            func(context.Context, string, *protocol.JSONObject) error
	version, platform string
	service           *connectors.Service
	mcp               *mcpruntime.Service
	closed            bool
}

func (r *desktopConnectorRuntime) get(ctx context.Context) (*connectors.Service, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, context.Canceled
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	if r.service != nil {
		return r.service, nil
	}
	n := r.native
	secure, err := securestorage.New(securestorage.Options{Home: n.Home, ConfigDir: n.Environment["CLAUDE_CONFIG_DIR"], ConfigDirExplicit: n.ConfigDirExplicit, Platform: r.platform, Environment: n.Environment, RunSecurity: r.security})
	if err != nil {
		return nil, err
	}
	options, err := r.settings.PluginOptions(secure)
	if err != nil {
		return nil, err
	}
	auth, err := mcpauth.New(mcpauth.Options{Secure: secure, Home: n.Home, ConfigDir: n.Environment["CLAUDE_CONFIG_DIR"], CWD: n.CWD, Platform: r.platform, Environment: n.Environment, Settings: func(ctx context.Context) (protocol.JSONObject, error) {
		value, err := r.settings.InitialSettings()
		if value == nil {
			return protocol.NewJSONObject(), err
		}
		return value.Clone(), err
	}})
	if err != nil {
		return nil, err
	}
	mcp, err := mcpruntime.New(mcpruntime.Options{CWD: n.CWD, Version: r.version, Environment: n.Environment, Enabled: r.settings.MCPExecutionEnabled, Authenticate: auth.Authenticate, ClearOAuth: auth.Clear, Auth: func(ctx context.Context, name string, config protocol.JSONObject) (mcpclient.AuthProvider, error) {
		if config.String("type") != "http" && config.String("type") != "sse" {
			return nil, nil
		}
		return auth.Provider(ctx, name, config)
	}})
	if err != nil {
		return nil, err
	}
	service, err := r.settings.Connectors(clibootstrap.NativeConnectorOptions{MCP: mcp, PluginOptions: options, Settings: r.editable, ReloadSessions: r.reload})
	if err != nil {
		_ = mcp.Close(context.Background())
		return nil, err
	}
	r.mcp, r.service = mcp, service
	return service, nil
}

func (r *desktopConnectorRuntime) close(ctx context.Context) error {
	r.mu.Lock()
	r.closed = true
	service, mcp := r.service, r.mcp
	r.mu.Unlock()
	var failures []error
	if service != nil {
		failures = append(failures, service.Close(ctx))
	}
	if mcp != nil {
		failures = append(failures, mcp.Close(ctx))
	}
	failures = append(failures, r.settings.Close(ctx))
	return errors.Join(failures...)
}
