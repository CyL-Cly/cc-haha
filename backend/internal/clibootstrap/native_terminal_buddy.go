package clibootstrap

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/buddy"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func terminalBuddyProjection(global protocol.JSONObject) (protocol.JSONObject, error) {
	companion, err := buddy.Get(global)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	value := protocol.NewJSONObject(fieldMain("muted", nativeSettingsTruthy(global.Get("companionMuted"))))
	if companion != nil {
		value.Set("companion", rawMain(*companion))
	} else {
		value.Set("companion", []byte("null"))
		preview, _, err := buddy.Roll(buddy.UserID(global), true)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		value.Set("preview", rawMain(preview))
	}
	return value, nil
}

func (m *MainPrint) TerminalBuddySnapshot(ctx context.Context) (json.RawMessage, error) {
	var result json.RawMessage
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if !m.terminalBuddyEnabled() {
			return cooperative.ErrRestricted
		}
		global, err := m.Global.Get()
		if err != nil {
			return err
		}
		value, err := terminalBuddyProjection(*global)
		if err == nil {
			result = rawMain(value)
		}
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}

func (m *MainPrint) terminalBuddyEnabled() bool {
	return m != nil && m.Host != nil && !m.closed && m.options.Native != nil && m.options.Interactive && !m.agentChild && (m.teamWorker == nil || m.teamWorker.pane != nil)
}

func (m *MainPrint) ApplyTerminalBuddy(ctx context.Context, args string) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if !m.terminalBuddyEnabled() {
			return cooperative.ErrRestricted
		}
		global, err := m.Global.Get()
		if err != nil {
			return err
		}
		action, err := buddy.Apply(*global, args, m.options.Native.NowMillis, nativeMathRandom)
		if err != nil {
			return err
		}
		if action.Global != nil {
			if err = m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
				next := current.Clone()
				for _, key := range []string{"companion", "companionMuted"} {
					before, after := global.Get(key), action.Global.Get(key)
					if string(before) == string(after) {
						continue
					}
					if len(after) == 0 {
						next.Delete(key)
					} else {
						next.Set(key, after)
					}
				}
				return &next, nil
			}); err != nil {
				return err
			}
			global, err = m.Global.Get()
			if err != nil {
				return err
			}
		}
		result, err = terminalBuddyProjection(*global)
		if err != nil {
			return err
		}
		result.Set("done", rawMain(action.Done))
		if len(action.Output) > 0 {
			result.Set("output", action.Output)
		}
		if action.PetAt != nil {
			result.Set("petAt", rawMain(*action.PetAt))
		}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}
