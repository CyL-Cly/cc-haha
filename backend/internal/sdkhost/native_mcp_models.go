package sdkhost

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelyield"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/websearch"
)

// NativeMCPModels exposes the existing two secondary-model producers to the
// non-ASK entrypoint. It constructs no ASK, agent, transcript or permission
// controller. The original request/stream/accounting and owner authority remain
// the same producers used by the native Web tools in the interactive host.
type NativeMCPModels struct{ host *Host }

func NewNativeMCPModels(scope *cooperative.Scope, options Options, sessionID string, permission func(*cooperative.Scope) (*ownerasync.OwnerFuture[protocol.JSONObject], error), selectModel func(*cooperative.Scope, modelselection.Options) (*modelselection.Selector, error)) (*NativeMCPModels, error) {
	if !scope.BelongsTo(options.Owner) || options.Controller != nil || options.Print == nil || !options.Print.NativeProcess || options.Print.Tools == nil || !options.Print.Tools.Owns(options.Owner) || !options.Print.Tools.HasWebFetchBindings() || !options.Print.Tools.HasWebSearchBindings() || options.Factory == nil || options.Global == nil || options.Startup == nil || options.UUID == nil || options.Now == nil || options.NowMillis == nil || options.TLSState == nil || sessionID == "" || permission == nil || selectModel == nil {
		return nil, cooperative.ErrRestricted
	}
	environment, err := NativePrintRequestEnvironment(options.OriginalEnv)
	if err != nil {
		return nil, err
	}
	policy, err := producerPolicyFromOriginalEnvironment(options.OriginalEnv, true)
	if err != nil {
		return nil, err
	}
	accounting, err := modelaccounting.NewOwnedSession(options.Owner, nil)
	if err != nil {
		return nil, err
	}
	err = accounting.InstallNativeScopedCalculator(scope, func(current *cooperative.Scope, model string, environment map[string]string) (*modelaccounting.Calculator, error) {
		return modelaccounting.NewNativeCalculator(model, modelaccounting.NativeCalculatorOptions{Environment: environment, Settings: options.Startup.GetInitialSettings, Global: options.Global.Get, SDKBetas: options.SDKBetas, ModelSelector: func(o modelselection.Options) (*modelselection.Selector, error) { return selectModel(current, o) }})
	})
	if err != nil {
		return nil, err
	}
	agent, err := modelaccounting.NewMainContext(options.Owner)
	if err != nil {
		return nil, err
	}
	ignored, err := ownerasync.NewIgnoredRejections(options.Owner)
	if err != nil {
		return nil, err
	}
	authority, err := cooperative.NewNativeMCPAuxiliaryModels(scope, options.Owner)
	if err != nil {
		return nil, err
	}
	host := &Host{options: options, sessionAccounting: accounting, agentContext: agent, permissionRejections: ignored, requestEnvironment: environment, streamPolicy: policy, streamClock: &modelyield.ProducerClock{NowMillis: options.NowMillis}, nativeMCPAuxiliaryModels: authority, mcpModelSessionID: sessionID, mcpModelPermission: permission}
	return &NativeMCPModels{host}, nil
}
func (m *NativeMCPModels) ApplyWebFetchPrompt(ctx context.Context, scope *cooperative.Scope, prompt json.RawMessage, model string, nonInteractive bool) (json.RawMessage, error) {
	if m == nil {
		return nil, cooperative.ErrRestricted
	}
	return m.host.ApplyWebFetchPrompt(ctx, scope, prompt, model, nonInteractive)
}
func (m *NativeMCPModels) NativeWebSearch(ctx context.Context, scope *cooperative.Scope, input protocol.JSONObject, model string, nonInteractive bool, progress websearch.Progress) ([]protocol.JSONObject, error) {
	if m == nil {
		return nil, cooperative.ErrRestricted
	}
	return m.host.NativeWebSearch(ctx, scope, input, model, nonInteractive, progress)
}
func (h *Host) webModelSessionID() string {
	if h.mcpModelSessionID != "" {
		return h.mcpModelSessionID
	}
	return h.options.Controller.SessionID()
}
func (h *Host) webModelPermission(scope *cooperative.Scope) (*ownerasync.OwnerFuture[protocol.JSONObject], error) {
	if h.mcpModelPermission != nil {
		return h.mcpModelPermission(scope)
	}
	return h.options.Controller.SuccessPermissionOnOwner(scope)
}

// Ordinary MCP's default AppState takes settings from the actual initial cache;
// its getter and merged getter are that same reference before immutable transfer.
func (m *NativeMCPModels) TransferNativeSettings(scope *cooperative.Scope) (NativeSettingsTransfer, error) {
	if m == nil || !scope.BelongsTo(m.host.options.Owner) {
		return NativeSettingsTransfer{}, cooperative.ErrRestricted
	}
	settings, e := m.host.options.Startup.GetInitialSettings()
	if e != nil {
		return NativeSettingsTransfer{}, e
	}
	if settings == nil {
		return NativeSettingsTransfer{}, cooperative.ErrRestricted
	}
	permission := protocol.NewJSONObject(protocol.JSONField{Name: "mode", Value: []byte(`"default"`)}, protocol.JSONField{Name: "additionalWorkingDirectories", Value: []byte(`{}`)}, protocol.JSONField{Name: "alwaysAllowRules", Value: []byte(`{}`)}, protocol.JSONField{Name: "alwaysDenyRules", Value: []byte(`{}`)}, protocol.JSONField{Name: "alwaysAskRules", Value: []byte(`{}`)}, protocol.JSONField{Name: "isBypassPermissionsModeAvailable", Value: []byte(`false`)})
	return NativeSettingsTransfer{settings: settings.Clone(), merged: settings.Clone(), permission: permission, proof: &nativeSettingsProof{sameReference: true}}, nil
}
func (m *NativeMCPModels) ApplyLedgerDelta(scope *cooperative.Scope, delta modelaccounting.LedgerDelta) error {
	if m == nil || !scope.BelongsTo(m.host.options.Owner) {
		return cooperative.ErrRestricted
	}
	return m.host.sessionAccounting.ApplyLedgerDelta(scope, delta)
}
func (m *NativeMCPModels) OutputTokens(scope *cooperative.Scope) (float64, error) {
	if m == nil || !scope.BelongsTo(m.host.options.Owner) {
		return 0, cooperative.ErrRestricted
	}
	snapshot, e := m.host.sessionAccounting.Snapshot(scope)
	var out float64
	if e == nil {
		e = json.Unmarshal(snapshot.Get("totalOutputTokens"), &out)
	}
	return out, e
}
func BorrowNativeMCPModels(scope *cooperative.Scope, h *Host) (*NativeMCPModels, error) {
	if h == nil || !scope.BelongsTo(h.options.Owner) || h.nativeAuxiliaryModels == nil || h.options.Print == nil || !h.options.Print.NativeProcess || h.options.Controller == nil {
		return nil, cooperative.ErrRestricted
	}
	return &NativeMCPModels{host: h}, nil
}

func (h *Host) runNativeWebProducer(scope *cooperative.Scope, ctx context.Context, source string, produce func(*cooperative.Scope) error) error {
	if h.nativeMCPAuxiliaryModels != nil {
		return h.nativeMCPAuxiliaryModels.Run(scope, ctx, source, produce)
	}
	return h.nativeAuxiliaryModels.Run(scope, ctx, source, produce)
}
