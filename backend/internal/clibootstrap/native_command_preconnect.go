package clibootstrap

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"runtime"

	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providernative"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

// Concrete factory identities, not JSON or a caller-provided callback. Manual
// auth/MCP facades and a substituted Owner cannot acquire this init consumer.
type nativeCommandInitBinding struct {
	owner       *cooperative.Owner
	native      *NativeBoundary
	settings    *NativeProcessSettings
	environment *clistartupenv.Environment
}

// This is the real Commander preAction caller, separate from constructing a
// leaf command or executing its already-admitted protocol Run callback.
func (m *MainCommand) BeginNativeAPIPreconnect(ctx context.Context) error {
	if m == nil || m.nativeCommandInit == nil || m.Native == nil || !m.Native.NativeProcess || m.Owner == nil || m.Settings == nil || m.Settings.environment == nil {
		return cooperative.ErrRestricted
	}
	bound := m.nativeCommandInit
	if bound.owner != m.Owner || bound.native != m.Native || bound.settings != m.Settings || bound.environment != m.Settings.environment {
		return cooperative.ErrRestricted
	}
	err := m.Owner.Run(ctx, func(scope *cooperative.Scope) (returned error) {
		// Commander awaits the memoized async init return even when its HEAD
		// remains pending (or was skipped). This is that fulfilled reaction,
		// never an await of the detached transport or a native readiness flush.
		defer func() {
			if returned == nil {
				returned = scope.AwaitFulfilled()
			}
		}()
		state := &m.nativeAPIPreconnect
		state.life.mu.Lock()
		closed := state.life.closed
		state.life.mu.Unlock()
		if closed {
			return cooperative.ErrClosed
		}
		if state.fired {
			return nil
		}
		// init.ts records this before creating the detached HEAD request.
		if err := m.Global.RecordFirstStartTime(); err != nil {
			return err
		}
		state.fired = true
		env := map[string]string{}
		for _, property := range m.Settings.environment.Snapshot().OwnProperties() {
			var name, value string
			if json.Unmarshal(property.NameJSON, &name) == nil && json.Unmarshal(property.Value, &value) == nil {
				env[name] = value
			}
		}
		state.environment = env
		target, err := nativeAPIPreconnectTarget(env)
		if err != nil || target == "" {
			return err
		}
		if m.nativeInitTLS == nil {
			m.nativeInitTLS, err = tlsconfig.NewState(func(path string) ([]byte, error) { return m.Native.readFile(m.Native.nativePhysicalPath(path)) })
			if err != nil {
				return err
			}
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = func(request *http.Request) (*url.URL, error) {
			selection := provider.ResolveNativeProxySelection(request.URL.String(), env)
			if selection.ProxyURL == "" || selection.Bypass {
				return nil, nil
			}
			return url.Parse(selection.ProxyURL)
		}
		if err := tlsconfig.ValidateOriginalTLSOptions(env); err != nil {
			return nil // the original detached fetch rejection is caught
		}
		certificates, err := m.nativeInitTLS.Certificates(env)
		if err != nil {
			return nil
		}
		transport.TLSClientConfig, err = tlsconfig.VerifiedConfigFromCertificates(certificates)
		if err != nil {
			return nil
		}
		client := &http.Client{Transport: transport}
		if runtime.GOOS == "darwin" {
			state.mu.Lock()
			if state.pool == nil {
				state.pool, err = providernative.NewPool(m.Owner, scope)
			}
			pool := state.pool
			state.mu.Unlock()
			if err != nil {
				return err
			}
			client.Transport, err = pool.HeadTransport(m.Owner, scope, &provider.RawHTTP1Exchange{VerifiedTLS: transport.TLSClientConfig})
			if err != nil {
				return err
			}
		}
		binding, err := m.nativeInitRefs.Bind(m.Owner)
		if err != nil {
			return err
		}
		return dispatchNativeAPIPreconnectOnOwner(scope, m.Owner, state, target, client, m.Native.PreconnectHTTP, binding)
	})
	if err != nil {
		return err
	}
	if err := m.BeginNativeCommandSinks(ctx); err != nil {
		return err
	}
	if err := m.BeginNativeCommandMigrations(ctx); err != nil {
		return err
	}
	return m.beginRemoteManagedPolicy(ctx)
}

// Agents has its own public action caller but borrows the same actual command
// factory. A finite leaf has no native init effect.
func (m *MainAgents) BeginNativeAPIPreconnect(ctx context.Context) error {
	if m == nil {
		return cooperative.ErrRestricted
	}
	if m.command == nil {
		return nil
	}
	return m.command.BeginNativeAPIPreconnect(ctx)
}

func (m *MainCommand) closeNativeAPIPreconnect() {
	state := &m.nativeAPIPreconnect
	state.life.close()
	state.mu.Lock()
	done, pool := state.done, state.pool
	state.mu.Unlock()
	if done != nil {
		<-done
	}
	if count, idle := m.nativeInitRefs.Snapshot(); count != 0 {
		<-idle // real completion/reaction release, outside the canonical Owner
	}
	if pool != nil {
		_ = pool.Close()
	}
}
