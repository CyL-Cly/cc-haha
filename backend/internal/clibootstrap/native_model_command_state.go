package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
	"strings"
	"sync"
)

// nativeModelCommandRuntime holds original process fast org/cache/cooldown
// state. No command can replace availability with a precomputed boolean.
type nativeModelCommandRuntime struct {
	fast           *retrypolicy.FastState
	orgEnabled     bool
	headerLatched  bool
	orgReason      string
	statusCancel   context.CancelFunc
	statusDone     <-chan struct{}
	statusComplete *ownerasync.OwnerFuture[struct{}]
	jobsMu         sync.Mutex
	jobs           []<-chan struct{}
}

func (m *MainPrint) initializeNativeModelCommandState(scope *cooperative.Scope) error {
	if !scope.BelongsTo(m.Owner) || m.options.Native == nil || !m.options.Native.NativeProcess {
		return cooperative.ErrRestricted
	}
	if m.modelCommandRuntime != nil {
		return nil
	}
	if err := m.initializePicker(scope); err != nil {
		return err
	}
	global, err := m.Global.Get()
	if err != nil {
		return err
	}
	runtime := &nativeModelCommandRuntime{fast: &retrypolicy.FastState{}, orgEnabled: true}
	env, err := m.pickerEnvironment()
	if err != nil {
		return err
	}
	settings, err := m.Startup.GetInitialSettings()
	if err != nil {
		return err
	}
	configured := settings != nil && string(settings.Get("fastMode")) == "true" && !modelCommandTruthy(settings.Get("fastModePerSessionOptIn"))
	flag, err := m.Startup.GetSettingsForSource("flagSettings")
	if err != nil {
		return err
	}
	sdkAllowed := m.options.Interactive || flag != nil && modelCommandTruthy(flag.Get("fastMode"))
	if pickerTrue(env["CLAUDE_CODE_SIMPLE"]) {
		runtime.orgEnabled = env["USER_TYPE"] == "ant" || global != nil && string(global.Get("penguinModeOrgEnabled")) == "true"
		if !runtime.orgEnabled {
			runtime.orgReason = "unknown"
		}
	}
	fast := configured && sdkAllowed && runtime.orgEnabled && !pickerTrue(env["CLAUDE_CODE_DISABLE_FAST_MODE"]) && nativeCommandFastSupported(m.nativeModelSelector, m.modelPicker.rawModel) && nativeCommandFirstParty(env)
	if err = m.Controller.InitializeTypedModelState(scope, m.modelPicker.rawModel, fast); err != nil {
		return err
	}
	m.modelCommandRuntime = runtime
	return nil
}
func nativeCommandFirstParty(env map[string]string) bool {
	for _, name := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		if pickerTrue(env[name]) {
			return false
		}
	}
	return true
}
func nativeCommandFastSupported(selector *modelselection.Selector, raw *string) bool {
	resolved, err := nativePickerResolve(selector, raw)
	return err == nil && strings.Contains(strings.ToLower(resolved), "opus-4-6")
}
func modelCommandTruthy(raw []byte) bool {
	return len(raw) > 0 && string(raw) != "null" && string(raw) != "false" && string(raw) != "0" && string(raw) != `""`
}
func nativeCommandExtraBilling(model *string, fast, subscriber, merged bool, env map[string]string) bool {
	if !subscriber {
		return false
	}
	if fast {
		return true
	}
	if model == nil || pickerTrue(env["CLAUDE_CODE_DISABLE_1M_CONTEXT"]) {
		return false
	}
	lower := strings.ToLower(*model)
	if !strings.Contains(lower, "[1m]") && !strings.HasSuffix(lower, ":1m") {
		return false
	}
	base := strings.TrimSpace(strings.TrimSuffix(lower, "[1m]"))
	opus := base == "opus" || strings.Contains(base, "opus-4-6")
	sonnet := base == "sonnet" || strings.Contains(base, "sonnet-4-6")
	return !(opus && merged) && (opus || sonnet)
}

func (m *MainPrint) nativeModelCommandSettingsBound(scope *cooperative.Scope) bool {
	return scope.BelongsTo(m.Owner) && m.options.Native != nil && m.options.Native.NativeProcess && m.nativeModelSelector != nil && m.Controller != nil && m.Controller.BootstrapOwns(m.Owner)
}
