package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/errorlog"
	"github.com/NanmiCoder/cc-haha/backend/internal/lspruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

// The sole startup call is after trust and inline plugin setup. Ordinary native
// children borrow this process manager and its diagnostic registry through the
// actual private detached factory, never a JSON/environment availability gate.
func (m *MainPrint) initializeNativeLSPOnOwner(scope *cooperative.Scope, binding *ownerasync.NativeBinding) error {
	if !scope.BelongsTo(m.Owner) || binding == nil || !binding.Owns(m.Owner) {
		return cooperative.ErrRestricted
	}
	m.nativeLSPLife.mu.Lock()
	closed := m.nativeLSPLife.closed
	m.nativeLSPLife.mu.Unlock()
	if closed {
		return cooperative.ErrClosed
	}
	if m.nativeLSP != nil || !m.options.Native.NativeProcess {
		return nil
	}
	if d := m.options.DetachedAgent; d != nil && d.authParent != nil {
		if !m.Controller.NativeAgentSkillPreloadProfile(scope, m.Owner, binding) {
			return cooperative.ErrRestricted
		}
		parent := d.authParent
		shared, err := nativeAgentOwnerCall(m, scope, parent, func(current *cooperative.Scope) (*lspruntime.Manager, error) {
			if parent.nativeLSP == nil {
				return nil, nil
			}
			if err := parent.nativeLSP.BindNativeChild(current, m.Owner, binding); err != nil {
				return nil, err
			}
			return parent.nativeLSP, nil
		})
		if err != nil {
			return err
		}
		m.nativeLSP = shared
		m.nativeLSPLife.borrowed = true
		m.nativeLSPLife.binding = binding
		return nil
	}
	if nativeEnvTruthy(m.options.Native.Environment["CLAUDE_CODE_SIMPLE"]) {
		return nil
	}
	if m.options.Interactive {
		trusted, err := m.nativeGoalTrust(scope)
		if err != nil {
			return err
		}
		if !trusted {
			return nil
		}
	}
	env, err := m.nativeProcessEnvironment(scope)
	if err != nil {
		return err
	}
	manager, err := lspruntime.New(lspruntime.Options{Owner: m.Owner, Binding: binding, CWD: m.options.Native.CWD, PID: m.options.PID, Environment: env, OnError: m.reportNativeLSPError, OnDebug: m.reportNativeLSPDebug, InteractiveCatalog: m.options.Interactive})
	if err != nil {
		return err
	}
	m.nativeLSP = manager
	m.nativeLSPLife.binding = binding
	return m.beginNativeLSPConfiguration(scope, 0)
}
func (m *MainPrint) beginNativeLSPConfiguration(scope *cooperative.Scope, generation uint64) error {
	life := &m.nativeLSPLife
	life.mu.Lock()
	if life.closed || life.borrowed || m.nativeLSP == nil {
		life.mu.Unlock()
		return nil
	}
	life.jobs.Add(1)
	binding := life.binding
	life.mu.Unlock()
	ref, err := binding.Acquire(scope)
	if err != nil {
		life.jobs.Done()
		return err
	}
	manager := m.nativeLSP
	go func() {
		defer life.jobs.Done()
		defer ref.DeliveryFinished()
		configurations, err := m.nativeLSPConfigurations(manager.Context())
		receipt, failure := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(current *cooperative.Scope) error {
			manager.ConfigureGeneration(generation, configurations, err)
			// Source headless's baseTools were sampled before LSP startup.
			// The interactive REPL assembles its current pool after startup.
			if m.options.Interactive && m.Host != nil && !m.shuttingDown && manager.Enabled() {
				return m.Host.RefreshNativeToolCatalog(current)
			}
			return nil
		})
		if failure == nil {
			failure = receipt.Wait()
		}
		if failure != nil {
			manager.ConfigureGeneration(generation, nil, failure)
		}
	}()
	return nil
}
func (m *MainPrint) reinitializeNativeLSPOnOwner(scope *cooperative.Scope) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	if m.nativeLSP == nil || m.nativeLSPLife.borrowed {
		return nil
	}
	return m.beginNativeLSPConfiguration(scope, m.nativeLSP.BeginConfiguration())
}
func (m *MainPrint) reportNativeLSPError(err error) {
	if err == nil {
		return
	}
	snapshot := errorlog.Details(err)
	receipt, failure := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(scope *cooperative.Scope) error {
		return m.nativeLogError(context.WithValue(context.Background(), nativeOpenAIAuthScopeKey{}, scope), snapshot)
	})
	if failure == nil {
		_ = receipt.Wait()
	}
}

// BeginNativeLSPClose precedes Host.Join so actual pending initialization,
// protocol requests and native notification writes cannot hold that join.
func (m *MainPrint) BeginNativeLSPClose() {
	if m == nil {
		return
	}
	life := &m.nativeLSPLife
	life.mu.Lock()
	life.closed = true
	borrowed, binding, manager := life.borrowed, life.binding, m.nativeLSP
	life.mu.Unlock()
	if manager == nil {
		return
	}
	if borrowed {
		manager.ReleaseNativeChild(m.Owner, binding)
	} else {
		manager.BeginClose()
	}
}
func (m *MainPrint) closeNativeLSP(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.BeginNativeLSPClose()
	m.nativeLSPLife.jobs.Wait()
	if m.nativeLSPLife.borrowed || m.nativeLSP == nil {
		return nil
	}
	return m.nativeLSP.Close(ctx)
}

func (m *MainPrint) reportNativeLSPDebug(message string) {
	m.reportNativeLSPDebugLevel(message, "DEBUG")
}

func (m *MainPrint) reportNativeLSPDebugLevel(message, level string) {
	receipt, err := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(*cooperative.Scope) error {
		if err := m.initializeNativeErrorLog(); err != nil {
			return err
		}
		if m.errorLogSink != nil {
			return m.errorLogSink.Debug(message, level)
		}
		return nil
	})
	if err == nil {
		_ = receipt.Wait()
	}
}
