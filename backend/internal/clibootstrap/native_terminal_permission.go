package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainPrint) CycleTerminalPermissionMode(ctx context.Context) (string, error) {
	var mode string
	receipt, e := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if m.Host == nil || m.options.Native == nil || m.agentChild || m.teamWorker != nil {
			return cooperative.ErrRestricted
		}
		var e error
		if m.nativeAutoConsent != nil {
			if m.nativeAutoConsent.Startup {
				return mainCap("Auto mode startup consent is pending")
			}
			m.nativeAutoConsent = nil
			mode = "default"
		} else {
			mode, e = m.Controller.NextTerminalPermissionMode(scope, m.options.Native.Environment["USER_TYPE"])
		}
		if e != nil {
			return e
		}
		if mode == "auto" && m.options.Interactive {
			opted, err := m.Controller.NativeAutoModeOptIn(scope)
			if err != nil {
				return err
			}
			if !opted {
				current, err := m.Host.NativePermissionContext(scope)
				if err != nil {
					return err
				}
				m.nativeAutoConsent = &NativeAutoConsent{ID: uuidMain(), Previous: current.String("mode")}
				if m.options.OnPermissionModeChanged != nil {
					return m.options.OnPermissionModeChanged(ctx, mode)
				}
				return nil
			}
		}
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
