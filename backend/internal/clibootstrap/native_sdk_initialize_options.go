package clibootstrap

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func sdkInitializeString(raw json.RawMessage) (string, error) {
	if string(raw) == "null" {
		return "", nil
	}
	units, e := protocol.DecodeJavaScriptJSONString(raw)
	if e != nil {
		return "", e
	}
	// Keep the original JSON string in the actual prompt binding. This string
	// is used only by the existing presence/context gate and process options.
	encoded := protocol.QuoteJavaScriptUTF16(units)
	var result string
	e = json.Unmarshal(encoded, &result)
	return result, e
}
func (m *MainPrint) nativeSDKInitializeOptions(scope *cooperative.Scope, request protocol.JSONObject) error {
	if !scope.BelongsTo(m.Owner) || m.Host == nil || m.Controller == nil || m.prompt == nil {
		return cooperative.ErrRestricted
	}
	if value := request.Get("systemPrompt"); value != nil {
		text, e := sdkInitializeString(value)
		if e != nil {
			return e
		}
		if e = m.Controller.SetNativeSDKSystemPrompt(scope, value, m.prompt.ResolveForModel); e != nil {
			return e
		}
		m.options.Arguments.SystemPrompt = text
		m.options.Arguments.HasCustomSystemPrompt = openaiauth.AuthTruthy(value)
	}
	if value := request.Get("appendSystemPrompt"); value != nil {
		text, e := sdkInitializeString(value)
		if e != nil {
			return e
		}
		if e = m.Host.SetNativeSDKAppendPrompt(scope, text); e != nil {
			return e
		}
		m.options.Arguments.AppendSystemPrompt = text
	}
	if value := request.Get("agents"); openaiauth.AuthTruthy(value) {
		if m.agents == nil {
			return cooperative.ErrRestricted
		}
		if e := m.agents.AppendSDKDefinitions(scope, value); e != nil {
			return e
		}
		if e := m.Host.RefreshNativeToolCatalog(scope); e != nil {
			return e
		}
	}
	if value := request.Get("jsonSchema"); openaiauth.AuthTruthy(value) {
		binding, e := m.Host.InstallNativeSDKJSONSchema(scope, value)
		if e != nil {
			return e
		}
		m.nativeJSONSchema = binding
		m.nativeSynthetic = binding.Synthetic()
	}
	return nil
}
