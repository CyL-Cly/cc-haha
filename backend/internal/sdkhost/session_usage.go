package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (h *Host) NativeSessionUsage(scope *cooperative.Scope) (protocol.JSONObject, error) {
	if !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || h.sessionAccounting == nil {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	added, removed, err := h.options.Print.Write.LineChanges(scope)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	resolver := modelaccounting.RestoredModelResolver{Environment: h.options.OriginalEnv, Settings: h.options.Startup.GetInitialSettings, Global: h.options.Global.Get, SDKBetas: h.options.SDKBetas}
	return h.sessionAccounting.UsageSnapshot(scope, modelaccounting.CostPresentation{NowMillis: h.options.NowMillis, StartMillis: h.options.Print.ProcessStartMillis, LinesAdded: added, LinesRemoved: removed, CanonicalName: resolver.Canonical})
}

func (h *Host) sessionUsageControl(scope *cooperative.Scope, input protocol.JSONObject) error {
	value, failure := h.NativeSessionUsage(scope)
	response := protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: quote("success")}, protocol.JSONField{Name: "request_id", Value: input.Get("request_id")})
	if failure != nil {
		response.Set("subtype", quote("error"))
		response.Set("error", quote(failure.Error()))
	} else {
		response.Set("response", raw(value))
	}
	message := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("control_response")}, protocol.JSONField{Name: "response", Value: raw(response)})
	data, err := protocol.EncodeNDJSON(message)
	if err != nil {
		return err
	}
	h.options.OnPhase("FIFO:enqueue")
	return h.options.Output.Enqueue(data)
}
