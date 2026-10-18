package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (m *MainPrint) ApplyNativeTeammateModelPicker(ctx context.Context, id string, result tui.ModelPickerResult) (bool, error) {
	changed := false
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if m.modelPicker == nil || m.modelPicker.pending == nil || m.modelPicker.pending.ID != id || !m.modelPicker.pendingTeammate {
			return mainCap("stale teammate model picker response")
		}
		picker := m.modelPicker.pending
		m.modelPicker.pending = nil
		m.modelPicker.pendingTeammate = false
		if result.Cancelled {
			return nil
		}
		valid := false
		for _, option := range picker.Options {
			valid = valid || pickerSame(option.Value, result.Model)
		}
		if !valid {
			return mainCap("teammate model selection outside original options")
		}
		global, err := m.Global.Get()
		if err != nil {
			return err
		}
		previous := global.Get("teammateDefaultModel")
		if len(previous) == 0 && result.Model == nil {
			return nil
		}
		// Source marks Config dirty after every explicit/default selection except
		// the first unset-to-null confirmation, even if the store skips a write.
		changed = true
		value := rawMain(result.Model)
		if string(previous) == string(value) {
			return nil
		}
		if err := m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			next := current.Clone()
			next.Set("teammateDefaultModel", value)
			return &next, nil
		}); err != nil {
			return err
		}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return changed, err
}
