package clibootstrap

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ideintegration"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/runtimecleanup"
)

type nativeIDERuntime struct {
	ctx           context.Context
	searchCancel  context.CancelFunc
	searchDone    chan struct{}
	searchService *ideintegration.Service
	terminal      string
	environment   map[string]string
	manualKind    string
	cancel        context.CancelFunc
	done          chan struct{}
	mu            sync.Mutex
	closed        bool
	once          sync.Once
	work          sync.WaitGroup
	extensions    *ideintegration.Extensions
}

func (s *nativeIDERuntime) stop() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.cancel()
		s.work.Wait()
		close(s.done)
	})
}
func (s *nativeIDERuntime) active() bool { s.mu.Lock(); defer s.mu.Unlock(); return !s.closed }
func (m *MainPrint) startNativeIDEIntegration(scope *cooperative.Scope) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	if m.ideIntegration != nil || !m.options.Native.NativeProcess || !m.options.Interactive || m.agentChild || m.teamWorker != nil {
		return nil
	}
	var e error
	environment := map[string]string{}
	for k, v := range m.options.Native.Environment {
		environment[k] = v
	}
	terminal := ideintegration.Terminal(environment)
	configDir := m.options.Native.ConfigHomeDirectory()
	cwd := m.state.Snapshot().OriginalCWD
	service := ideintegration.NewNative(configDir, cwd, m.options.Platform, environment, os.Getppid())
	ctx, cancel := context.WithCancel(context.Background())
	state := &nativeIDERuntime{ctx: ctx, searchService: service, terminal: terminal, environment: environment, cancel: cancel, done: make(chan struct{})}
	state.extensions = ideintegration.NewNativeExtensions(m.options.Native.Home, cwd, m.options.Platform, provider.SourceCompatibilityVersion, environment, os.Getppid())
	m.ideIntegration = state
	if _, e = m.Cleanup.Register(scope, runtimecleanup.NewCallback(func(scope *cooperative.Scope) (*ownerasync.OwnerFuture[struct{}], error) {
		return nativeSkillWork(m, scope, func() (struct{}, error) { state.stop(); return struct{}{}, nil })
	})); e != nil {
		state.stop()
		return e
	}
	m.startNativeIDESearch(state)
	global, e := m.Global.Get()
	if e != nil {
		state.stop()
		return e
	}
	var autoInstall *bool
	if raw := global.Get("autoInstallIdeExtension"); raw != nil && string(raw) != "null" {
		value := openaiauth.AuthTruthy(raw)
		autoInstall = &value
	}
	if ideintegration.AutoInstall(autoInstall, environment) && ideintegration.SupportedTerminal(terminal, environment) {
		state.work.Add(1)
		go func() { defer state.work.Done(); m.runNativeIDEExtension(ctx, state, terminal) }()
	}
	return nil
}

func (s *nativeIDERuntime) installRequested() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.manualKind != ""
}
func (m *MainPrint) startNativeIDESearch(state *nativeIDERuntime) {
	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		return
	}
	if state.searchCancel != nil {
		state.searchCancel()
	}
	ctx, cancel := context.WithCancel(state.ctx)
	state.searchCancel = cancel
	done := make(chan struct{})
	state.searchDone = done
	state.work.Add(1)
	state.mu.Unlock()
	go func() {
		defer state.work.Done()
		defer close(done)
		ide, e := state.searchService.Find(ctx)
		if e != nil || ide == nil || ctx.Err() != nil {
			return
		}
		receipt, failure := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(current *cooperative.Scope) error {
			if !state.active() || ctx.Err() != nil || m.closed || m.shuttingDown {
				return nil
			}
			global, err := m.Global.Get()
			if err != nil {
				return err
			}
			if !ideintegration.AutoConnect(m.options.Arguments.IDE || state.installRequested(), openaiauth.AuthTruthy(global.Get("autoConnectIde")), state.terminal, state.environment) || openaiauth.AuthTruthy(m.nativeDynamicMCP.Get("ide")) {
				return nil
			}
			config := nativeIDEConfig(*ide)
			m.nativeDynamicMCP.Set("ide", rawMain(config))
			if m.mcpTools != nil {
				if e := m.mcpTools.Discover(current); e != nil {
					return e
				}
			}
			if !state.active() || m.closed || m.shuttingDown {
				return nil
			}
			return m.Host.RefreshNativeToolCatalog(current)
		})
		if failure != nil && ctx.Err() == nil {
			m.reportNativeIDEFailure(failure)
		} else if failure == nil {
			// Keep rejection handling off Owner: awaiting a queued callback here
			// would deadlock an owner-lifetime cleanup that joins the search.
			go func() {
				if err := receipt.Wait(); err != nil && state.active() && ctx.Err() == nil {
					m.reportNativeIDEFailure(err)
				}
			}()
		}
	}()
}

func (m *MainPrint) reportNativeIDEFailure(err error) {
	m.outputMu.Lock()
	defer m.outputMu.Unlock()
	_, _ = fmt.Fprintf(m.options.Error, "IDE integration failed: %v\n", err)
}
