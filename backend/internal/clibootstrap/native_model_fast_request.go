package clibootstrap

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

func (m *MainPrint) nativeFastModeBinding(scope *cooperative.Scope) (*sdkhost.NativeFastModeBinding, error) {
	// The original disabled feature has no runtime/request producer. In
	// particular a SideQuestion continuation cannot borrow main AppState.
	if pickerTrue(m.nativeOpenAIEnvironment()["CLAUDE_CODE_DISABLE_FAST_MODE"]) {
		return nil, nil
	}
	if err := m.initializeNativeModelCommandState(scope); err != nil {
		return nil, err
	}
	runtime := m.modelCommandRuntime
	effect := func(value retrypolicy.FastEffect) error { return m.nativeModelFastEffect(scope, value) }
	proof, err := cooperative.NewNativeFastModeRequest(m.Owner, scope, func(model string, requested *bool) (bool, bool, error) {
		env, err := m.pickerEnvironment()
		if err != nil {
			return false, false, err
		}
		state, err := m.Controller.TypedModelStateOnOwner(scope)
		if err != nil {
			return false, false, err
		}
		current, err := retrypolicy.ReadFastState(runtime.fast, m.options.Native.Now(), !pickerTrue(env["CLAUDE_CODE_DISABLE_FAST_MODE"]), effect)
		if err != nil {
			return false, false, err
		}
		fast := requested != nil && *requested && state.FastMode && runtime.orgEnabled && nativeCommandFirstParty(env) && !pickerTrue(env["CLAUDE_CODE_DISABLE_FAST_MODE"]) && nativeCommandFastSupported(m.nativeModelSelector, pickerPointer(model)) && current.Status != "cooldown"
		if fast {
			runtime.headerLatched = true
		}
		return fast, runtime.headerLatched, nil
	})
	if err != nil {
		return nil, err
	}
	return &sdkhost.NativeFastModeBinding{Proof: proof, State: runtime.fast, Effect: effect}, nil
}
