package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// TerminalCostFieldsOnOwner captures the original current-session fields before
// switching bootstrap SID. It borrows the same ledger and actual Write counters.
func (h *Host) TerminalCostFieldsOnOwner(scope *cooperative.Scope) (protocol.JSONObject, error) {
	if h == nil || scope == nil || !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess || !h.options.Print.NativeInteractive || h.askRunning || h.closing {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	added, removed, err := h.options.Print.Write.LineChanges(scope)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	return h.sessionAccounting.PersistedCostFields(scope, h.options.Controller.SessionID(), modelaccounting.CostPresentation{NowMillis: h.options.NowMillis, StartMillis: h.options.Print.ProcessStartMillis, LinesAdded: added, LinesRemoved: removed})
}

func (h *Host) ResetTerminalCostsOnOwner(scope *cooperative.Scope) error {
	if h == nil || scope == nil || !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess || !h.options.Print.NativeInteractive || h.askRunning || h.closing {
		return cooperative.ErrRestricted
	}
	if err := h.sessionAccounting.ResetForTerminalSwitch(scope, h.options.NowMillis); err != nil {
		return err
	}
	return h.options.Print.Write.ResetTerminalLineChanges(scope)
}
