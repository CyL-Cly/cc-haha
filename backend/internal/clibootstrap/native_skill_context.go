package clibootstrap

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainPrint) nativeToolPermissionContext(scope *cooperative.Scope) (protocol.JSONObject, error) {
	if m.Host != nil {
		return m.Host.NativePermissionContext(scope)
	}
	_, context, err := m.Controller.SDKSettingsOnOwner(scope)
	return context, err
}
