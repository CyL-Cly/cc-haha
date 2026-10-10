package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

// QueryEngine forwards this recovery envelope without adding it to model
// history or transcripts. Clients clear the failed in-flight deltas before a
// later demand starts the next response. Agents consume it without SDK output.
func (h *Host) nativeStreamRecovery(scope *cooperative.Scope, message queryloop.Message) (requestretry.NoticeAction, error) {
	if message.Record.String("cause") != "stream_retry" {
		return 0, cap("actual streaming recovery cause")
	}
	if h.isAgent() {
		return requestretry.Resume, nil
	}
	return h.enqueue(scope, protocol.NewJSONObject(
		protocol.JSONField{Name: "type", Value: quote("system")},
		protocol.JSONField{Name: "subtype", Value: quote("streaming_fallback")},
		protocol.JSONField{Name: "cause", Value: message.Record.Get("cause")},
		protocol.JSONField{Name: "session_id", Value: quote(h.options.Controller.SessionID())},
		protocol.JSONField{Name: "uuid", Value: message.Record.Get("uuid")},
	))
}
