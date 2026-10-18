package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type nativeTerminalMCPAuthLifetime struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	closed bool
	// Configured before authentication starts; observes without taking ownership.
	onCallbackListener func(net.Listener)
}

func (life *nativeTerminalMCPAuthLifetime) bind(parent context.Context) (context.Context, func()) {
	life.mu.Lock()
	if life.ctx == nil {
		life.ctx, life.cancel = context.WithCancel(context.Background())
		if life.closed {
			life.cancel()
		}
	}
	global := life.ctx
	life.mu.Unlock()
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(global, cancel)
	if global.Err() != nil {
		cancel()
	}
	return ctx, func() { stop(); cancel() }
}
func (life *nativeTerminalMCPAuthLifetime) close() {
	life.mu.Lock()
	defer life.mu.Unlock()
	life.closed = true
	if life.cancel != nil {
		life.cancel()
	}
}

func (m *MainPrint) BeginNativeTerminalMCPAuthClose() {
	if m != nil {
		m.nativeTerminalMCPAuthLife.close()
		if m.mcpTools != nil {
			m.mcpTools.CloseAuthenticationTools()
		}
	}
}

func (m *MainPrint) terminalMCPAuthenticationTarget(ctx context.Context, scope *cooperative.Scope, name string) (protocol.JSONObject, error) {
	config, err := m.terminalMCPRemoteTarget(ctx, scope, name)
	if err != nil {
		return config, err
	}
	if config.String("type") != "http" && config.String("type") != "sse" {
		return protocol.JSONObject{}, errors.New("Server does not use remote MCP OAuth: " + name)
	}
	return config, nil
}

func (m *MainPrint) terminalMCPRemoteTarget(ctx context.Context, scope *cooperative.Scope, name string) (protocol.JSONObject, error) {
	if m == nil || scope == nil || !scope.BelongsTo(m.Owner) || m.options.Native == nil || !m.options.Native.NativeProcess || !m.options.Interactive || m.mcpBorrowed || m.agentChild || m.closed || m.shuttingDown || m.mcpTools == nil || m.mcpRuntime == nil {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	config, err := m.mcpTools.TerminalAuthenticationTarget(ctx, scope, name)
	if err != nil {
		return config, err
	}
	return config, nil
}

// The terminal supplies its real dialog context and callbacks. Physical OAuth,
// manual callback validation, credential writes and reconnect all remain on
// the same runtime/secure-store graph; global close cancels before native join.
func (m *MainPrint) StartTerminalMCPAuthentication(ctx context.Context, name string, onURL func(string), onWaiting func(func(string))) error {
	return m.StartTerminalMCPAuthenticationWithState(ctx, name, false, onURL, onWaiting)
}

// The two Source callers differ: MCPSettings prepares isAuthenticated, while
// ManagePlugins explicitly leaves it undefined. Only that caller projection
// controls the pre-consent revoke; connected tool count is a separate label.
func (m *MainPrint) StartTerminalMCPAuthenticationWithState(ctx context.Context, name string, wasAuthenticated bool, onURL func(string), onWaiting func(func(string))) error {
	if m == nil || m.Owner == nil || ctx == nil {
		return cooperative.ErrRestricted
	}
	ctx, release := m.nativeTerminalMCPAuthLife.bind(ctx)
	defer release()
	var physicalDone chan struct{}
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		config, err := m.terminalMCPAuthenticationTarget(ctx, scope, name)
		if err != nil {
			return err
		}
		physicalDone = make(chan struct{})
		pending, err := nativeSkillWork(m, scope, func() (struct{}, error) {
			defer close(physicalDone)
			if wasAuthenticated {
				if err := m.mcpRuntime.RevokeOAuth(ctx, name, config, true); err != nil {
					return struct{}{}, err
				}
			}
			return struct{}{}, m.mcpRuntime.AuthenticateWithOptions(ctx, name, config, func(address string) error {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if onURL != nil {
					onURL(address)
				}
				return nil
			}, mcpauth.FlowOptions{OnCallbackListener: m.nativeTerminalMCPAuthLife.onCallbackListener, OnWaitingForCallback: func(submit func(string)) {
				if onWaiting != nil {
					onWaiting(func(address string) {
						if ctx.Err() == nil {
							submit(address)
						}
					})
				}
			}})
		})
		if err != nil {
			physicalDone = nil
			return err
		}
		if _, err = pending.Await(scope); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := m.mcpTools.ReconnectTerminal(ctx, scope, name); err != nil {
			return err
		}
		return m.publishNativeMCPCatalog(scope)
	})
	if err == nil {
		err = receipt.Wait()
	}
	// A cancelled Scope stops awaiting its continuation before physical work
	// necessarily returns. Source abort cleanup closes the callback listener
	// before settling consent. Join that real cleanup outside the Owner.
	if physicalDone != nil {
		if err != nil {
			release()
		}
		<-physicalDone
	}
	return err
}
func (m *MainPrint) ClearTerminalMCPAuthentication(ctx context.Context, name string) error {
	if m == nil || m.Owner == nil || ctx == nil {
		return cooperative.ErrRestricted
	}
	ctx, release := m.nativeTerminalMCPAuthLife.bind(ctx)
	defer release()
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		config, err := m.terminalMCPAuthenticationTarget(ctx, scope, name)
		if err != nil {
			return err
		}
		if err := m.mcpTools.ClearTerminalAuthentication(ctx, scope, name, config); err != nil {
			return err
		}
		return m.publishNativeMCPCatalog(scope)
	})
	if err == nil {
		err = receipt.Wait()
	}
	return err
}
func (m *MainPrint) TerminalMCPHasAuthentication(ctx context.Context, name string) (bool, error) {
	if m == nil || m.Owner == nil || ctx == nil {
		return false, cooperative.ErrRestricted
	}
	var authenticated bool
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		config, err := m.terminalMCPAuthenticationTarget(ctx, scope, name)
		if err != nil {
			return err
		}
		pending, err := nativeSkillWork(m, scope, func() (bool, error) { return m.mcpRuntime.HasOAuthTokens(ctx, name, config) })
		if err != nil {
			return err
		}
		authenticated, err = pending.Await(scope)
		if err != nil || authenticated {
			return err
		}
		rows, err := m.mcpTools.Status(scope)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.String("name") == name && row.String("status") == "connected" {
				var tools []json.RawMessage
				_ = json.Unmarshal(row.Get("tools"), &tools)
				authenticated = m.nativeMCPIngressToken() != "" || len(tools) > 0
			}
		}
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	return authenticated, err
}
