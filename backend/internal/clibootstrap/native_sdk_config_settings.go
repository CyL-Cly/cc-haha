package clibootstrap

import (
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type nativeSDKConfigSettings struct {
	mu     sync.RWMutex
	reader *clistartupenv.NativeSDKSettingsReader
}

func (m *MainPrint) bindNativeSDKConfigSettings(scope *cooperative.Scope) error {
	if m == nil || !scope.BelongsTo(m.Owner) || m.options.Native == nil || !m.options.Native.NativeProcess || m.options.Interactive || m.agentChild || m.Host == nil {
		return cooperative.ErrRestricted
	}
	if _, err := m.Host.InferenceModel(scope); err != nil {
		return err
	}
	m.nativeSDKSettings.mu.Lock()
	defer m.nativeSDKSettings.mu.Unlock()
	if m.nativeSDKSettings.reader != nil {
		_, err := m.nativeSDKSettings.reader.InitialSettings()
		return err
	}
	reader, err := m.Startup.BindNativeSDKSettingsReader(m.settings, m.settingState, m.environment)
	if err == nil {
		err = m.Host.BindNativeSDKSettingsReader(scope, reader)
	}
	if err == nil {
		m.nativeSDKSettings.reader = reader
	}
	return err
}

func (m *MainPrint) nativeSDKConfigReader() *clistartupenv.NativeSDKSettingsReader {
	m.nativeSDKSettings.mu.RLock()
	defer m.nativeSDKSettings.mu.RUnlock()
	return m.nativeSDKSettings.reader
}

func (m *MainPrint) nativeSDKInitialSettings() (*protocol.JSONObject, error) {
	if reader := m.nativeSDKConfigReader(); reader != nil {
		return reader.InitialSettings()
	}
	return m.Startup.GetInitialSettings()
}

func (m *MainPrint) nativeSDKSourceSettings(source string) (*protocol.JSONObject, error) {
	if reader := m.nativeSDKConfigReader(); reader != nil {
		return reader.SourceSettings(source)
	}
	return m.Startup.GetSettingsForSource(source)
}

func (m *MainPrint) nativeSDKSettingsWithSources() (protocol.JSONObject, error) {
	if reader := m.nativeSDKConfigReader(); reader != nil {
		return reader.SettingsWithSources()
	}
	return m.Startup.GetSettingsWithSources()
}
