package clibootstrap

import (
	"context"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func (m *MainPrint) terminalFullscreenBound(scope *cooperative.Scope) bool {
	return m != nil && !m.closed && m.initialized && m.Host != nil && m.options.Interactive && !m.agentChild && m.nativeStatusLineSettingsBound(scope)
}

// Fullscreen reads the actual process environment, including committed settings
// changes. The physical tmux result remains the existing process-local memo.
func (m *MainPrint) ReadTerminalFullscreen(ctx context.Context) (bool, error) {
	if m == nil || m.Owner == nil {
		return false, cooperative.ErrRestricted
	}
	var fullscreen bool
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if !m.terminalFullscreenBound(scope) {
			return cooperative.ErrRestricted
		}
		var err error
		fullscreen, err = m.terminalFullscreenOnOwner(ctx, scope)
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	return fullscreen, err
}

// Reuse the process settings-change listeners, independently of whether the
// StatusLine component is mounted or configured. Its existing publisher runs
// only after the actual settings consumer has committed environment changes.
// The listener only wakes the terminal; it never reenters the Owner.
func (m *MainPrint) SubscribeNativeTerminalFullscreen(ctx context.Context, changed func()) (bool, func(), error) {
	if m == nil || m.Owner == nil {
		return false, nil, cooperative.ErrRestricted
	}
	var fullscreen bool
	var id uint64
	runtime := &m.nativeTerminalStatusLine
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if !m.terminalFullscreenBound(scope) {
			return cooperative.ErrRestricted
		}
		var err error
		fullscreen, err = m.terminalFullscreenOnOwner(ctx, scope)
		if err != nil {
			return err
		}
		runtime.mu.Lock()
		defer runtime.mu.Unlock()
		if runtime.closed {
			return cooperative.ErrClosed
		}
		if changed != nil {
			if runtime.listeners == nil {
				runtime.listeners = map[uint64]func(){}
			}
			runtime.next++
			id = runtime.next
			runtime.listeners[id] = changed
		}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	if err != nil {
		return false, nil, err
	}
	var once sync.Once
	return fullscreen, func() { once.Do(func() { runtime.mu.Lock(); delete(runtime.listeners, id); runtime.mu.Unlock() }) }, nil
}
