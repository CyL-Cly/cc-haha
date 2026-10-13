package sdkhost

import (
	"encoding/json"
	"math"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

func (h *Host) maximumUSDBudget() *float64 {
	if h.isAgent() || h.options.Print == nil || h.options.Print.NativeInteractive {
		return nil
	}
	return h.options.Print.MaxBudgetUSD
}

func budgetNumber(value float64) string {
	if math.IsInf(value, 1) {
		return "Infinity"
	}
	encoded := json.RawMessage(strconv.FormatFloat(value, 'g', -1, 64))
	quoted, _ := protocol.JavaScriptStringJSON(encoded)
	var text string
	_ = json.Unmarshal(quoted, &text)
	return text
}

// QueryEngine tests cumulative process cost only after consuming an original
// yield. A first plain-text completion can charge in its producer finally after
// its last yield, and is therefore not converted into a budget error here.
func (h *Host) stopAtUSDBudget(scope *cooperative.Scope, usage protocol.JSONObject, lastStop json.RawMessage, start float64, turns int, checkpoint func() error) (bool, error) {
	budget := h.maximumUSDBudget()
	if budget == nil {
		return false, nil
	}
	snapshot, err := h.sessionAccounting.Snapshot(scope)
	if err != nil || !(num(snapshot.Get("totalCostUSD")) >= *budget) {
		return false, err
	}
	if err := checkpoint(); err != nil {
		return false, err
	}
	duration, err := h.sessionAccounting.DurationSnapshot(scope)
	if err != nil {
		return false, err
	}
	result := protocol.NewJSONObject(
		protocol.JSONField{Name: "type", Value: quote("result")},
		protocol.JSONField{Name: "subtype", Value: quote("error_max_budget_usd")},
		protocol.JSONField{Name: "duration_ms", Value: raw(h.options.NowMillis() - start)},
		protocol.JSONField{Name: "duration_api_ms", Value: duration.Get("totalAPIDuration")},
		protocol.JSONField{Name: "is_error", Value: raw(true)},
		protocol.JSONField{Name: "num_turns", Value: raw(turns)},
		protocol.JSONField{Name: "stop_reason", Value: lastStop},
		protocol.JSONField{Name: "session_id", Value: quote(h.options.Controller.SessionID())},
		protocol.JSONField{Name: "total_cost_usd", Value: snapshot.Get("totalCostUSD")},
		protocol.JSONField{Name: "usage", Value: raw(usage)},
		protocol.JSONField{Name: "modelUsage", Value: snapshot.Get("modelUsage")},
		protocol.JSONField{Name: "permission_denials", Value: h.permissionDenialsJSON()},
		protocol.JSONField{Name: "fast_mode_state", Value: quote("off")},
		protocol.JSONField{Name: "uuid", Value: quote(h.options.UUID())},
		protocol.JSONField{Name: "errors", Value: raw([]string{"Reached maximum budget ($" + budgetNumber(*budget) + ")"})},
	)
	_, err = h.enqueue(scope, result)
	return true, err
}

func (h *Host) nativeRetryPolicyEnvironment() map[string]string {
	if h.options.Print == nil || !h.options.Print.NativeProcess {
		return nil
	}
	result := map[string]string{}
	for _, key := range []string{"FALLBACK_FOR_ALL_PRIMARY_MODELS", "USER_TYPE", "IS_SANDBOX", "CLAUDE_CODE_REMOTE"} {
		if value, exists := h.options.OriginalEnv[key]; exists {
			result[key] = value
		}
	}
	return result
}

func (h *Host) fallbackModel() string {
	if h.options.Print == nil || h.options.Print.NativeInteractive {
		return ""
	}
	return h.options.Print.FallbackModel
}

func (h *Host) applyBudgetAfterYield(scope *cooperative.Scope, action requestretry.NoticeAction, err error, usage protocol.JSONObject, lastStop json.RawMessage, start float64, turns int, checkpoint func() error) (requestretry.NoticeAction, error) {
	if err != nil || action != requestretry.Resume {
		return action, err
	}
	stopped, err := h.stopAtUSDBudget(scope, usage, lastStop, start, turns, checkpoint)
	if stopped || err != nil {
		return requestretry.Stop, err
	}
	return action, nil
}
