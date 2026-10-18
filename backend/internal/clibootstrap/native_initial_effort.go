package clibootstrap

import "encoding/json"

// Source main uses explicit CLI effort before the persisted runtime setting.
// External persisted xhigh/max and numeric values remain session scoped.
func (m *MainPrint) initialNativeEffort() (json.RawMessage, error) {
	if m.agentChild {
		return append(json.RawMessage(nil), m.options.AgentEffort...), nil
	}
	value := nativeEffortValue(m.options.Arguments.EffortLevel)
	if value != nil || !m.options.Native.NativeProcess || m.teamWorker != nil && m.teamWorker.pane == nil {
		return value, nil
	}
	if m.mainThreadAgent != nil && m.mainThreadAgent.definition != nil && m.mainThreadAgent.definition.Effort != nil {
		return append(json.RawMessage(nil), m.mainThreadAgent.definition.Effort...), nil
	}
	settings, err := m.nativeSDKInitialSettings()
	if err != nil || settings == nil {
		return nil, err
	}
	level := settings.String("effortLevel")
	if level == "low" || level == "medium" || level == "high" || level == "max" && m.options.Native.Environment["USER_TYPE"] == "ant" {
		return append(json.RawMessage(nil), settings.Get("effortLevel")...), nil
	}
	return nil, nil
}
