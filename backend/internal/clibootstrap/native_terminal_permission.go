package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
)

func (m *MainPrint) CycleTerminalPermissionMode(ctx context.Context) (string, error) {
	var mode string
	receipt, e := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if m.Host == nil || m.options.Native == nil || m.agentChild || m.teamWorker != nil {
			return cooperative.ErrRestricted
		}
		current, e := m.Host.NativePermissionContext(scope)
		if e != nil {
			return e
		}
		if current.String("mode") == "auto" || current.String("prePlanMode") == "auto" || current.Get("strippedDangerousRules") != nil {
			return mainCap("terminal permission cycle active auto classifier")
		}
		mode = sdkpermissions.NextTerminalMode(current, m.options.Native.Environment["USER_TYPE"])
		if e = m.Host.SetNativePermissionMode(scope, mode); e != nil {
			return e
		}
		if mode == "plan" {
			e = m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
				copy := current.Clone()
				copy.Set("lastPlanModeUse", rawMain(float64(m.options.Native.Now().UnixMilli())))
				return &copy, nil
			})
			if e != nil {
				return e
			}
		}
		if m.options.OnPermissionModeChanged != nil {
			return m.options.OnPermissionModeChanged(ctx, mode)
		}
		return nil
	})
	if e == nil {
		e = receipt.Wait()
	}
	return mode, e
}
