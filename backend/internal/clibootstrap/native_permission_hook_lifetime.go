package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func (m *MainPrint) nativePermissionHookRetention() func(*cooperative.Scope, context.CancelFunc) (bool, error) {
	d := m.options.DetachedAgent
	if d == nil {
		return nil
	}
	parent := d.authParent
	if parent == nil && d.preset != nil {
		parent = d.preset.parent
	}
	if parent == nil {
		return nil
	}
	return func(scope *cooperative.Scope, cancel context.CancelFunc) (bool, error) {
		if parent.Host == nil {
			return false, cooperative.ErrRestricted
		}
		return nativeAgentOwnerCall(m, scope, parent, func(next *cooperative.Scope) (bool, error) {
			return parent.Host.RetainNativePermissionHookSignal(next, cancel)
		})
	}
}
