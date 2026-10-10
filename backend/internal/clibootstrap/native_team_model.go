package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// AcknowledgeTeamWorkerModel performs the real prepared MAIN worker model
// control before release. It retains the original model-switch breadcrumbs,
// shared settings/permission context and worker construction identity.
func (m *MainPrint) AcknowledgeTeamWorkerModel(ctx context.Context, model string) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if m.teamWorker == nil || m.Host == nil || !m.initialized || m.closed || model == "" {
			return cooperative.ErrRestricted
		}
		settings, err := m.Startup.GetInitialSettings()
		if err != nil {
			return err
		}
		resolved, selector, err := m.resolveNativeModel(ctx, scope, settings, model)
		if err != nil {
			return err
		}
		display, err := nativePickerDisplay(selector, &model)
		if err != nil {
			return err
		}
		if err = m.Host.ApplyNativeTeamModel(scope, model, resolved, display); err != nil {
			return err
		}
		m.nativeModelSelector = selector
		m.options.Arguments.Model = model
		permissions, err := m.Host.NativePermissionContext(scope)
		if err != nil {
			return err
		}
		result = protocol.NewJSONObject(protocol.JSONField{Name: "model", Value: quoteMain(resolved)}, protocol.JSONField{Name: "permissionMode", Value: permissions.Get("mode")})
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}
