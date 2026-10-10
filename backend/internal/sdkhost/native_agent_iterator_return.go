package sdkhost

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

// ReturnAgentIterator queues the real AsyncGenerator return behind its pending
// next. It does not cancel the model transport or manufacture iterator completion.
// The caller can race this receipt with the original one-second cleanup grace.
func (h *Host) ReturnAgentIterator(scope *cooperative.Scope) (*OwnerFuture[struct{}], error) {
	if !scope.BelongsTo(h.options.Owner) || !h.isAgent() || h.options.Print == nil || !h.options.Print.NativeProcess {
		return nil, cooperative.ErrRestricted
	}
	bridge := h.agentQueryIterator
	if bridge != nil && bridge.agentReturnReceipt != nil {
		return bridge.agentReturnReceipt, nil
	}
	receipt, e := NewOwnerFuture[struct{}](h.options.Owner)
	if e != nil {
		return nil, e
	}
	if bridge == nil {
		// A naturally completed generator's return(undefined) is already done.
		return receipt, receipt.Resolve(scope, struct{}{})
	}
	bridge.agentReturnRequested = true
	if bridge.agentReturnReceipt == nil {
		bridge.agentReturnReceipt = receipt
		return receipt, nil
	}
	return bridge.agentReturnReceipt, nil
}
func (h *Host) agentIteratorReturnRequested() bool {
	return h.agentQueryIterator != nil && h.agentQueryIterator.agentReturnRequested
}

func nativeAgentIteratorExposes(value queryloop.Yield) bool {
	if value.Message == nil {
		return false
	}
	row := value.Message
	switch row.Type() {
	case "assistant", "user", "progress":
		return true
	case "system":
		return row.Record.String("subtype") == "compact_boundary"
	case "attachment":
		var attachment protocol.JSONObject
		_ = json.Unmarshal(row.Record.Get("attachment"), &attachment)
		return attachment.String("type") != "max_turns_reached"
	}
	return false
}
