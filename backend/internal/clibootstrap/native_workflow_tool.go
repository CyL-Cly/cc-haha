package clibootstrap

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainPrint) nativeWorkflowBindings() *builtintools.WorkflowBindings {
	if m.workflowDefinitions == nil {
		return nil
	}
	return &builtintools.WorkflowBindings{Enabled: m.workflowDefinitions.EnabledOnOwner, Launch: m.launchNativeWorkflow, Guideline: m.nativeWorkflowGuideline, AutoConsent: func(scope *cooperative.Scope) (bool, error) {
		if !scope.BelongsTo(m.Owner) {
			return false, cooperative.ErrRestricted
		}
		global, e := m.Global.Get()
		if e != nil {
			return false, nil
		}
		return string(global.Get("hasAcceptedWorkflowsInAutoMode")) == "true", nil
	}, Ultracode: m.nativeUltracodeActive}
}
func workflowGuidelineValue(value protocol.JSONObject) string {
	switch value.String("workflowSizeGuideline") {
	case "unrestricted", "small", "medium", "large":
		return value.String("workflowSizeGuideline")
	}
	return ""
}
func (m *MainPrint) nativeWorkflowGuideline(scope *cooperative.Scope) (string, error) {
	if !scope.BelongsTo(m.Owner) {
		return "", cooperative.ErrRestricted
	}
	policy, e := m.nativeSDKSourceSettings("policySettings")
	if e != nil {
		return "", e
	}
	if policy != nil {
		if value := workflowGuidelineValue(*policy); value != "" {
			return value, nil
		}
	}
	initial, e := m.nativeSDKInitialSettings()
	if e != nil {
		return "", e
	}
	if initial != nil {
		if value := workflowGuidelineValue(*initial); value != "" {
			return value, nil
		}
	}
	global, e := m.Global.Get()
	if e == nil {
		if value := workflowGuidelineValue(*global); value != "" {
			return value, nil
		}
	}
	return "medium", nil
}
