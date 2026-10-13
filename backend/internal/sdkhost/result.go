package sdkhost

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func (h *Host) resultOnOwner(scope *cooperative.Scope, last queryloop.Message, usage protocol.JSONObject, lastStop json.RawMessage, start float64, turns int) (protocol.JSONObject, error) {
	snapshot, err := h.sessionAccounting.Snapshot(scope)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	// A local output view only: never retained or charged by the Host.
	accounting := &Accounting{total: num(snapshot.Get("totalCostUSD")), models: object(snapshot.Get("modelUsage"))}
	duration, err := h.sessionAccounting.DurationSnapshot(scope)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	var including float64
	if err = json.Unmarshal(duration.Get("totalAPIDuration"), &including); err != nil {
		return protocol.JSONObject{}, err
	}
	result, e := projectResult(last, usage, lastStop, start, h.options.Controller.SessionID(), accounting, including, h.options.NowMillis, h.options.UUID, turns)
	if e == nil {
		result.Set("permission_denials", h.permissionDenialsJSON())
		h.nativeJSONSchemaResult(&result)
	}
	return result, e
}

// projectResult is the output projection after real ASK completion. It never
// runs a model, admits history, completes files or supplies resolved state.
func projectResult(last queryloop.Message, usage protocol.JSONObject, lastStop json.RawMessage, start float64, sessionID string, accounting *Accounting, apiMillis float64, now func() float64, uuid func() string, turns ...int) (protocol.JSONObject, error) {
	data, err := last.MarshalJSON()
	if err != nil {
		return protocol.JSONObject{}, err
	}
	envelope := object(data)
	message := object(envelope.Get("message"))
	var content []protocol.JSONObject
	_ = json.Unmarshal(message.Get("content"), &content)
	var final protocol.JSONObject
	if len(content) > 0 {
		final = content[len(content)-1]
	}
	success := false
	switch envelope.String("type") {
	case "assistant":
		kind := final.String("type")
		success = kind == "text" || kind == "thinking" || kind == "redacted_thinking"
	case "user":
		success = len(content) > 0
		for _, block := range content {
			if block.String("type") != "tool_result" {
				success = false
			}
		}
		if len(content) == 1 && content[0].String("type") == "text" && (content[0].String("text") == "[Request interrupted by user]" || content[0].String("text") == "[Request interrupted by user for tool use]") {
			success = true
		}
	}
	if string(lastStop) == `"end_turn"` {
		success = true
	}
	result := protocol.NewJSONObject()
	result.Set("type", quote("result"))
	subtype := "success"
	if !success {
		subtype = "error_during_execution"
	}
	result.Set("subtype", quote(subtype))
	apiError := string(envelope.Get("isApiErrorMessage")) == "true"
	if success {
		result.Set("is_error", raw(apiError))
	}
	result.Set("duration_ms", raw(now()-start))
	// The initial Source oracle freezes Date.now. Variable API/retry duration
	// instrumentation is not silently asserted by this finite host slice.
	result.Set("duration_api_ms", raw(apiMillis))
	if !success {
		result.Set("is_error", raw(true))
	}
	numTurns := 1
	if len(turns) > 1 || len(turns) == 1 && turns[0] < 1 {
		return protocol.JSONObject{}, cap("original per-ASK user-yield turn count")
	}
	if len(turns) == 1 {
		numTurns = turns[0]
	}
	result.Set("num_turns", raw(numTurns))
	if success {
		resultText := ""
		if envelope.String("type") == "assistant" && final.String("type") == "text" {
			resultText = final.String("text")
			switch resultText {
			case "[Request interrupted by user]", "[Request interrupted by user for tool use]",
				"The user doesn't want to take this action right now. STOP what you are doing and wait for the user to tell you how to proceed.",
				"The user doesn't want to proceed with this tool use. The tool use was rejected (eg. if it was a file edit, the new_string was NOT written to the file). STOP what you are doing and wait for the user to tell you how to proceed.", "No response requested.":
				resultText = ""
			}
		}
		result.Set("result", quote(resultText))
	}
	result.Set("stop_reason", lastStop)
	result.Set("session_id", quote(sessionID))
	result.Set("total_cost_usd", raw(accounting.total))
	result.Set("usage", raw(usage))
	result.Set("modelUsage", raw(accounting.models))
	result.Set("permission_denials", raw([]any{}))
	result.Set("fast_mode_state", quote("off"))
	result.Set("uuid", quote(uuid()))
	if !success {
		kind := envelope.String("type")
		if kind == "" {
			kind = "undefined"
		}
		contentType := "n/a"
		if kind == "assistant" {
			contentType = final.String("type")
			if contentType == "" {
				contentType = "none"
			}
		}
		stop := string(lastStop)
		if stop != "null" {
			stop = object([]byte(`{"x":` + stop + `}`)).String("x")
		}
		result.Set("errors", raw([]string{"[ede_diagnostic] result_type=" + kind + " last_content_type=" + contentType + " stop_reason=" + stop}))
	}
	return result, nil
}
