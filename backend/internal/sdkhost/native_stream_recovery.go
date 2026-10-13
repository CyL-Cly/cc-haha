package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

// QueryEngine retains system messages in mutableMessages and projects this
// signal separately from toSDKMessages. The current ASK's transcript slice does
// not include it; the next ASK records its initial mutable history normally.
func (h *Host) nativeStreamRecovery(scope *cooperative.Scope, message queryloop.Message) (requestretry.NoticeAction, error) {
	if !messages.IsNativeStreamRecoveryRecord(message.Record) {
		return 0, cap("actual streaming recovery cause")
	}
	if h.isAgent() {
		return requestretry.Resume, nil
	}
	if err := h.serial.Accept(scope, message); err != nil {
		return 0, err
	}
	return h.enqueue(scope, nativeStreamRecoveryOutput(message.Record, h.options.Controller.SessionID()))
}

func nativeStreamRecoveryOutput(message protocol.JSONObject, sessionID string) protocol.JSONObject {
	return protocol.NewJSONObject(
		protocol.JSONField{Name: "type", Value: quote("system")},
		protocol.JSONField{Name: "subtype", Value: quote("streaming_fallback")},
		protocol.JSONField{Name: "cause", Value: message.Get("cause")},
		protocol.JSONField{Name: "session_id", Value: quote(sessionID)},
		protocol.JSONField{Name: "uuid", Value: message.Get("uuid")},
	)
}
