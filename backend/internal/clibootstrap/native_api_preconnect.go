package clibootstrap

import (
	"context"
	"net/http"
	"runtime"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providernative"
)

// This state belongs to the normal CLI init caller. Constructing a leaf
// MainPrint/Session does not perform that caller's detached network effect.
type nativeAPIPreconnectState struct {
	fired       bool              // accessed only on the canonical Owner
	environment map[string]string // immutable safe-init transport snapshot
	mu          sync.Mutex
	life        nativeRemoteLifetime
	done        <-chan struct{}
	pool        *providernative.Pool
}

func (m *MainPrint) initializeNativeHTTPPool(scope *cooperative.Scope) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	if m.options.Native == nil || !m.options.Native.NativeProcess || runtime.GOOS != "darwin" {
		return nil
	}
	state := &m.nativeAPIPreconnect
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.pool == nil {
		if detached := m.options.DetachedAgent; detached != nil && detached.httpPool != nil {
			if !m.detachedConstructed || detached.authParent == nil || m.options.AgentID == "" ||
				!detached.httpPool.SourceOwns(detached.authParent.Owner) {
				return cooperative.ErrRestricted
			}
			var err error
			state.pool, err = detached.httpPool.BindOnOwner(m.Owner, scope)
			return err
		}
		var err error
		state.pool, err = providernative.NewPool(m.Owner, scope)
		return err
	}
	return nil
}

func (m *MainPrint) nativeHTTPPool() *providernative.Pool {
	m.nativeAPIPreconnect.mu.Lock()
	defer m.nativeAPIPreconnect.mu.Unlock()
	return m.nativeAPIPreconnect.pool
}

func nativeAPIPreconnectTarget(env map[string]string) (string, error) {
	for _, key := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		if envTruthyNative(env[key]) {
			return "", nil
		}
	}
	for _, key := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ANTHROPIC_UNIX_SOCKET", "CLAUDE_CODE_CLIENT_CERT", "CLAUDE_CODE_CLIENT_KEY"} {
		if env[key] != "" {
			return "", nil
		}
	}
	if base := env["ANTHROPIC_BASE_URL"]; base != "" {
		return base, nil
	}
	config, err := desktopoauth.ResolveConfig(env)
	return config.BaseAPIURL, err
}

// BeginNativeAPIPreconnect is invoked by the real normal CLI action after its
// settings environment and TLS setup. A tool/ASK cancellation does not cancel
// this init effect; global close joins it before closing the shared Owner.
func (m *MainPrint) BeginNativeAPIPreconnect(ctx context.Context) error {
	if m == nil || m.options.Native == nil || !m.options.Native.NativeProcess {
		return nil
	}
	if m.closed {
		return cooperative.ErrClosed
	}
	err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		state := &m.nativeAPIPreconnect
		if state.fired {
			return nil
		}
		state.fired = true
		env := state.environment
		if env == nil {
			env = m.nativeOpenAIEnvironment()
		}
		target, err := nativeAPIPreconnectTarget(env)
		if err != nil || target == "" {
			return err
		}
		client, err := m.nativeRemoteHTTP(env)
		if err != nil {
			// Native fetch rejects invalid transport setup; Source catches
			// that detached rejection instead of failing the next ASK.
			return nil
		}
		if err := m.initializeNativeHTTPPool(scope); err != nil {
			return err
		}
		if pool := m.nativeHTTPPool(); pool != nil {
			transport, ok := client.Transport.(*http.Transport)
			if !ok {
				return mainCap("init HEAD original TLS transport")
			}
			client.Transport, err = pool.HeadTransport(m.Owner, scope, &provider.RawHTTP1Exchange{VerifiedTLS: transport.TLSClientConfig})
			if err != nil {
				return err
			}
		}
		binding := m.commandBinding
		if binding == nil {
			if m.Controller == nil {
				return cooperative.ErrRestricted
			}
			binding, err = m.Controller.BorrowNativeCopyBinding(m.Owner)
			if err != nil {
				return err
			}
		}
		return dispatchNativeAPIPreconnectOnOwner(scope, m.Owner, state, target, client, m.options.Native.PreconnectHTTP, binding)
	})
	if err != nil {
		return err
	}
	return m.beginRemoteManagedPolicy(ctx)
}

func (m *MainPrint) BeginNativeAPIPreconnectClose() {
	if m != nil {
		m.nativeAPIPreconnect.life.close()
	}
}

func (m *MainPrint) joinNativeAPIPreconnect(ctx context.Context) error {
	m.nativeAPIPreconnect.mu.Lock()
	done := m.nativeAPIPreconnect.done
	m.nativeAPIPreconnect.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
