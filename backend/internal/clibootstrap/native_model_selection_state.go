package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
)

// Both original SetModelAndClose and ModelPickerWrapper share this AppState
// transition; the menu alone checks availability when choosing its ON notice.
func (m *MainPrint) applyNativeSelectedModelState(ctx context.Context, scope *cooperative.Scope, model *string, selector *modelselection.Selector, env map[string]string, menu bool) (string, error) {
	state, err := m.Controller.TypedModelStateOnOwner(scope)
	if err != nil {
		return "", err
	}
	fast := state.FastMode
	wasOn, wasOff := false, false
	suffix := ""
	if !pickerTrue(env["CLAUDE_CODE_DISABLE_FAST_MODE"]) {
		m.modelCommandRuntime.fast.ClearFastModeCooldown()
		if !nativeCommandFastSupported(selector, model) && fast {
			fast = false
			wasOff = true
		} else if nativeCommandFastSupported(selector, model) && fast && (!menu || m.modelCommandRuntime.orgEnabled) {
			wasOn = true
			suffix += " · Fast mode ON"
		}
	}
	identity, err := m.openAIAuth.AnthropicModelIdentity(ctx, scope, false)
	if err != nil {
		return "", err
	}
	merged := !pickerTrue(env["CLAUDE_CODE_DISABLE_1M_CONTEXT"]) && identity.SubscriptionType != "pro" && (!identity.Subscriber || identity.SubscriptionType != "") && nativeCommandFirstParty(env)
	if nativeCommandExtraBilling(model, wasOn, identity.Subscriber, merged, env) {
		suffix += " · Billed as extra usage"
	}
	if wasOff {
		suffix += " · Fast mode OFF"
	}
	if err = m.Controller.ApplyTypedModelState(scope, model, fast); err != nil {
		return "", err
	}
	if m.options.Interactive && !pickerTrue(env["CLAUDE_CODE_REMOTE"]) {
		if err = m.verifyNativeAutoGateOnOwner(scope); err != nil {
			return "", err
		}
	}
	return suffix, nil
}
