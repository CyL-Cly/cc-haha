package sdkhost

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

// This is the QueryEngine attachment consumer, after inline transcript admission
// and the checkpoint. It borrows the original cumulative session ledger.
func (h *Host) maxTurnsResultOnOwner(scope *cooperative.Scope, message queryloop.Message, usage protocol.JSONObject, lastStop json.RawMessage, start float64) (protocol.JSONObject, error) {
	a := object(message.Record.Get("attachment"))
	var max, turns int
	if message.Type() != "attachment" || a.String("type") != "max_turns_reached" || json.Unmarshal(a.Get("maxTurns"), &max) != nil || json.Unmarshal(a.Get("turnCount"), &turns) != nil || max < 1 || turns != max+1 {
		return protocol.JSONObject{}, errors.New("invalid original max-turn attachment")
	}
	snapshot, err := h.sessionAccounting.Snapshot(scope)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	duration, err := h.sessionAccounting.DurationSnapshot(scope)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	// Public/neutral admission currently supports only non-fast execution and
	// allow-only tool permission feedback; active denials remain refused upstream.
	return protocol.NewJSONObject(
		protocol.JSONField{Name: "type", Value: quote("result")},
		protocol.JSONField{Name: "subtype", Value: quote("error_max_turns")},
		protocol.JSONField{Name: "duration_ms", Value: raw(h.options.NowMillis() - start)},
		protocol.JSONField{Name: "duration_api_ms", Value: duration.Get("totalAPIDuration")},
		protocol.JSONField{Name: "is_error", Value: raw(true)},
		protocol.JSONField{Name: "num_turns", Value: raw(turns)},
		protocol.JSONField{Name: "stop_reason", Value: lastStop},
		protocol.JSONField{Name: "session_id", Value: quote(h.options.Controller.SessionID())},
		protocol.JSONField{Name: "total_cost_usd", Value: snapshot.Get("totalCostUSD")},
		protocol.JSONField{Name: "usage", Value: raw(usage)},
		protocol.JSONField{Name: "modelUsage", Value: snapshot.Get("modelUsage")},
		protocol.JSONField{Name: "permission_denials", Value: raw([]any{})},
		protocol.JSONField{Name: "fast_mode_state", Value: quote("off")},
		protocol.JSONField{Name: "uuid", Value: quote(h.options.UUID())},
		protocol.JSONField{Name: "errors", Value: raw([]string{fmt.Sprintf("Reached maximum number of turns (%d)", max)})},
	), nil
}
