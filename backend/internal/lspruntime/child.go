package lspruntime

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

// BindNativeChild is invoked by the actual detached MainPrint factory on its
// parent's current Owner. It shares the process singleton and exact native
// reference ledger, not configuration descriptors or an arbitrary callback.
func (m *Manager) BindNativeChild(scope *cooperative.Scope, owner *cooperative.Owner, binding *ownerasync.NativeBinding) error {
	if m == nil || owner == nil || binding == nil || !binding.Owns(owner) {
		return cooperative.ErrRestricted
	}
	if err := scope.Inline(cooperative.Read, func(*cooperative.Scope) error { return nil }); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return cooperative.ErrClosed
	}
	parent := scope.BelongsTo(m.options.Owner)
	if !parent {
		for existing, ledger := range m.children {
			if scope.BelongsTo(existing) && ledger.Owns(existing) {
				parent = true
				break
			}
		}
	}
	if !parent || scope.BelongsTo(owner) {
		return cooperative.ErrRestricted
	}
	if m.children == nil {
		m.children = map[*cooperative.Owner]*ownerasync.NativeBinding{}
	}
	m.children[owner] = binding
	return nil
}
func (m *Manager) ReleaseNativeChild(owner *cooperative.Owner, binding *ownerasync.NativeBinding) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.children[owner].Same(binding) {
		delete(m.children, owner)
	}
}
