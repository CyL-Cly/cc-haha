package clibootstrap

import (
	"bytes"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainPrint) nativeSDKPermissionControl(scope *cooperative.Scope, event protocol.JSONObject, enqueue func(*cooperative.Scope, protocol.JSONObject) error) error {
	if !scope.BelongsTo(m.Owner) || m.Host == nil || m.nativePermissionStartup == nil || enqueue == nil {
		return cooperative.ErrRestricted
	}
	request := nativeInProcessObject(event.Get("request"))
	mode := request.String("mode")
	current, err := m.Controller.PlanModeContext(scope)
	if err != nil {
		return err
	}
	reason, err := m.nativePermissionStartup.NativeSDKModeRejection(scope, mode, current)
	if err != nil {
		return err
	}
	response := protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: quoteMain("success")}, protocol.JSONField{Name: "request_id", Value: event.Get("request_id")})
	if reason != "" {
		response.Set("subtype", quoteMain("error"))
		response.Set("error", quoteMain(reason))
	} else {
		response.Set("response", rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "mode", Value: request.Get("mode")})))
	}
	// The original handler emits its response before setAppState commits the
	// mode and its central SDK status/CCR metadata observers run.
	if err = enqueue(scope, protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("control_response")}, protocol.JSONField{Name: "response", Value: rawMain(response)})); err != nil {
		return err
	}
	previousUltraplan := m.nativeUltraplan
	if flag := request.Get("ultraplan"); len(flag) > 0 && !bytes.Equal(bytes.TrimSpace(flag), []byte("null")) {
		_ = json.Unmarshal(flag, &m.nativeUltraplan)
	}
	m.nativeModeChangeUltraplan = m.nativeUltraplan && !previousUltraplan
	defer func() { m.nativeModeChangeUltraplan = false }()
	if reason != "" {
		return nil
	}
	return m.Host.SetNativePermissionMode(scope, mode)
}

func (m *MainPrint) bindNativePermissionModeObserver(scope *cooperative.Scope) error {
	if m.options.Native == nil || !m.options.Native.NativeProcess {
		return nil
	}
	return m.Controller.BindPermissionModeObserver(scope, func(scope *cooperative.Scope, previous, current string) error {
		oldExternal, newExternal := nativeRemoteExternalPermissionMode(previous), nativeRemoteExternalPermissionMode(current)
		if m.remoteIO != nil && oldExternal != newExternal {
			metadata := protocol.NewJSONObject(protocol.JSONField{Name: "permission_mode", Value: quoteMain(newExternal)}, protocol.JSONField{Name: "is_ultraplan_mode", Value: []byte("null")})
			if newExternal == "plan" && m.nativeModeChangeUltraplan {
				metadata.Set("is_ultraplan_mode", []byte("true"))
			}
			m.remoteIO.ReportMetadata(metadata)
		}
		if m.options.Interactive || nativeRemoteExternalPermissionMode(current) != current {
			return nil
		}
		event := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("system")}, protocol.JSONField{Name: "subtype", Value: quoteMain("status")}, protocol.JSONField{Name: "status", Value: []byte("null")}, protocol.JSONField{Name: "permissionMode", Value: quoteMain(current)}, protocol.JSONField{Name: "uuid", Value: quoteMain(uuidMain())}, protocol.JSONField{Name: "session_id", Value: quoteMain(m.Controller.SessionID())})
		return m.Host.EmitNativeSDKEvent(scope, event)
	})
}

func nativeRemoteExternalPermissionMode(mode string) string {
	switch mode {
	case "default", "acceptEdits", "bypassPermissions", "plan", "auto", "dontAsk":
		return mode
	}
	return "default"
}
