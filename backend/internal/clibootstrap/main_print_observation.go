package clibootstrap

import "github.com/NanmiCoder/cc-haha/backend/internal/provider"

func (m *MainPrint) mainUUID() string {
	if m.options.UUIDPrimitive != nil {
		return m.options.UUIDPrimitive()
	}
	return uuidMain()
}

func (m *MainPrint) sdkRuntime() provider.SourceRuntime {
	if m.options.SDKRuntime != nil {
		return *m.options.SDKRuntime
	}
	return provider.SourceRuntime{Platform: m.options.Platform, Arch: "arm64", Version: "v24.3.0"}
}

func (m *MainPrint) controllerPhase(phase string) {
	if m.options.OnControllerPhase != nil {
		m.options.OnControllerPhase(phase)
	}
}

func (m *MainPrint) sdkPhase(phase string) {
	if m.options.OnSDKPhase != nil {
		m.options.OnSDKPhase(phase)
	}
}
