package clibootstrap

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

// Original in-process agents import the same userProvidedImages module as the
// parent. Go child Harnesses retain that one authorization authority through
// immutable requests delivered to its actual Owner, never through a shared Set.
func (m *MainPrint) nativeUserImageAuthorized(scope *cooperative.Scope, path string) (bool, error) {
	if !scope.BelongsTo(m.Owner) {
		return false, cooperative.ErrRestricted
	}
	if detached := m.options.DetachedAgent; detached != nil {
		if detached.userImageAuthorized == nil {
			return false, mainCap("parent image authorization producer")
		}
		return detached.userImageAuthorized(m, scope, path)
	}
	return m.userImages.ContainsResolved(path), nil
}

func (m *MainPrint) nativeRememberUserImage(scope *cooperative.Scope, path string) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	if detached := m.options.DetachedAgent; detached != nil {
		if detached.rememberUserImage == nil {
			return mainCap("parent image authorization producer")
		}
		return detached.rememberUserImage(m, scope, path)
	}
	m.userImages.RegisterResolved(path)
	return nil
}

func (m *MainPrint) bindAgentImageAuthorization(detached *NativeDetachedAgentOptions) {
	detached.userImageAuthorized = func(child *MainPrint, scope *cooperative.Scope, path string) (bool, error) {
		return nativeAgentOwnerCall(child, scope, m, func(parentScope *cooperative.Scope) (bool, error) {
			return m.nativeUserImageAuthorized(parentScope, path)
		})
	}
	detached.rememberUserImage = func(child *MainPrint, scope *cooperative.Scope, path string) error {
		return bridgeAgentVoid(child, m, scope, func(parentScope *cooperative.Scope) error {
			return m.nativeRememberUserImage(parentScope, path)
		})
	}
}
