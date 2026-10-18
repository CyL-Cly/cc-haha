package clibootstrap

import (
	"context"
	"encoding/json"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainPrint) nativeModelSelection(ctx context.Context, scope *cooperative.Scope, options modelselection.Options) (*modelselection.Selector, error) {
	if m.openAIAuth != nil {
		active, err := m.openAIAuth.ModelActive(ctx, scope, !m.options.Interactive)
		if err != nil {
			return nil, err
		}
		if active {
			return modelselection.NewOpenAIAuthentication(options)
		}
		identity, err := m.openAIAuth.AnthropicModelIdentity(ctx, scope, !m.options.Interactive)
		if err != nil {
			return nil, err
		}
		if m.options.Native.NativeProcess && (m.options.Arguments.Remote != nil || m.options.Arguments.Teleport != "" || m.options.Arguments.TeleportWithoutValue) {
			return modelselection.NewNativeCCRRemoteAuthentication(options, identity)
		}
		return modelselection.NewAnthropicAuthentication(options, identity)
	}
	return modelselection.NewDirectCredentials(options)
}

func (m *MainPrint) resolveNativeModel(ctx context.Context, scope *cooperative.Scope, settings *protocol.JSONObject, requested string) (string, *modelselection.Selector, error) {
	options := nativeModelOptions(settings, m.nativeOpenAIEnvironment(), requested)
	selector, err := m.nativeModelSelection(ctx, scope, options)
	if err != nil {
		return "", nil, err
	}
	return selectNativeModel(selector)
}

func resolveNativeModel(settings *protocol.JSONObject, env map[string]string, requested string) (string, *modelselection.Selector, error) {
	selector, err := modelselection.NewDirectCredentials(nativeModelOptions(settings, env, requested))
	if err != nil {
		return "", nil, err
	}
	return selectNativeModel(selector)
}

func nativeModelOptions(settings *protocol.JSONObject, env map[string]string, requested string) modelselection.Options {
	merged := protocol.NewJSONObject()
	if settings != nil {
		merged = settings.Clone()
	}
	var override json.RawMessage
	if requested != "" {
		override = quoteMain(requested)
	}
	return modelselection.Options{Environment: env, Settings: merged, Override: override}
}

func selectNativeModel(selector *modelselection.Selector) (string, *modelselection.Selector, error) {
	raw, err := selector.Select()
	if err != nil {
		return "", nil, err
	}
	model, err := decodeNativeModel(raw)
	return model, selector, err
}

func decodeNativeModel(raw json.RawMessage) (string, error) {
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return "", err
	}
	for at := 0; at < len(units); at++ {
		unit := units[at]
		if unit >= 0xd800 && unit <= 0xdbff && at+1 < len(units) && units[at+1] >= 0xdc00 && units[at+1] <= 0xdfff {
			at++
			continue
		}
		if unit >= 0xd800 && unit <= 0xdfff {
			return "", mainCap("raw UTF16 model identity in request transport")
		}
	}
	return string(utf16.Decode(units)), nil
}
