package sdkhost

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// This is QueryEngine's real shouldQuery=false branch. It does not project a
// provider result or put the synthetic display assistant into persisted history.
func (h *Host) runLocalInput(scope *cooperative.Scope, local *sessioncontroller.LocalInputResult) error {
	return h.runLocalOutput(scope, local, true)
}

func (h *Host) runLocalOutput(scope *cooperative.Scope, local *sessioncontroller.LocalInputResult, display bool) error {
	first, e := h.serial.BeginRecord(scope, h.serial.History())
	if e != nil {
		return e
	}
	h.options.OnPhase("Record:local-before-init")
	if _, e = first.Await(scope); e != nil {
		return e
	}
	message, e := h.initMessageOnOwner(scope, h.options.Model)
	if e != nil {
		return e
	}
	action, e := h.enqueue(scope, message)
	if e != nil || action == requestretry.Stop {
		return e
	}
	if len(local.CompactHistory) > 0 {
		for _, record := range local.CompactHistory {
			if err := h.emitCompactOutput(scope, record); err != nil {
				return err
			}
		}
		display = false
	}
	if display {
		history := h.serial.History()
		if len(history) < 3 {
			return cap("local command real history")
		}
		system := history[len(history)-1].Record
		synthetic := h.options.Factory.CreateAssistant(storage.Object{"content": quote(local.Text)})
		output := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("assistant")}, protocol.JSONField{Name: "message", Value: synthetic["message"]}, protocol.JSONField{Name: "parent_tool_use_id", Value: json.RawMessage("null")}, protocol.JSONField{Name: "session_id", Value: quote(h.options.Controller.SessionID())}, protocol.JSONField{Name: "uuid", Value: system.Get("uuid")})
		action, e = h.enqueue(scope, output)
		if e != nil || action == requestretry.Stop {
			return e
		}
	}
	second, e := h.serial.BeginRecord(scope, h.serial.History())
	if e != nil {
		return e
	}
	h.options.OnPhase("Record:local-after-output")
	if _, e = second.Await(scope); e != nil {
		return e
	}
	snapshot, e := h.sessionAccounting.Snapshot(scope)
	if e != nil {
		return e
	}
	duration, e := h.sessionAccounting.DurationSnapshot(scope)
	if e != nil {
		return e
	}
	result := protocol.NewJSONObject(
		protocol.JSONField{Name: "type", Value: quote("result")}, protocol.JSONField{Name: "subtype", Value: quote("success")}, protocol.JSONField{Name: "is_error", Value: raw(false)}, protocol.JSONField{Name: "duration_ms", Value: raw(h.options.NowMillis() - local.StartMillis)}, protocol.JSONField{Name: "duration_api_ms", Value: duration.Get("totalAPIDuration")}, protocol.JSONField{Name: "ttft_ms", Value: raw(0)}, protocol.JSONField{Name: "decode_ms", Value: raw(0)}, protocol.JSONField{Name: "num_turns", Value: raw(h.serial.HistoryLength() - 1)}, protocol.JSONField{Name: "result", Value: quote(local.Text)}, protocol.JSONField{Name: "stop_reason", Value: json.RawMessage("null")}, protocol.JSONField{Name: "session_id", Value: quote(h.options.Controller.SessionID())}, protocol.JSONField{Name: "total_cost_usd", Value: snapshot.Get("totalCostUSD")}, protocol.JSONField{Name: "usage", Value: raw(zeroUsage())}, protocol.JSONField{Name: "modelUsage", Value: snapshot.Get("modelUsage")}, protocol.JSONField{Name: "permission_denials", Value: raw([]any{})}, protocol.JSONField{Name: "fast_mode_state", Value: quote("off")}, protocol.JSONField{Name: "uuid", Value: quote(h.options.UUID())})
	_, e = h.enqueue(scope, result)
	return e
}
