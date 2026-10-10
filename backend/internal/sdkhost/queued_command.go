package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

// This is QueryEngine's actual queued attachment consumer. Pending peer
// messages keep their delivery ID after compaction and acknowledge only after
// the owning canonical writer has completed Record and physical Flush.
func (h *Host) acceptQueuedCommand(scope *cooperative.Scope, message queryloop.Message, history *[]queryloop.Message, persist bool) (requestretry.NoticeAction, error) {
	attachment := object(message.Record.Get("attachment"))
	uuid := attachment.String("source_uuid")
	pending, e := h.options.Controller.SessionMessagePendingOnOwner(scope, uuid)
	if e != nil {
		return 0, e
	}
	if pending {
		message.Record.Set("uuid", attachment.Get("source_uuid"))
	}
	if e = h.serial.Accept(scope, message); e != nil {
		return 0, e
	}
	*history = append(*history, message)
	if persist {
		future, e := h.serial.BeginRecord(scope, *history)
		if e != nil {
			return 0, e
		}
		h.options.OnPhase("Record:returned")
		if pending {
			if _, e = future.Await(scope); e != nil {
				return 0, e
			}
			h.options.OnPhase("Record:fulfilled")
			flush, e := h.pending.Flush(scope)
			if e != nil {
				return 0, e
			}
			if e = flush.Await(scope); e != nil {
				return 0, e
			}
		} else {
			h.observeRecord(scope, future)
		}
	}
	if uuid != "" {
		if e = h.options.Controller.ConsumeSessionMessageOnOwner(scope, uuid); e != nil {
			return 0, e
		}
	}
	replay, e := h.options.Controller.ReplayUserMessagesOnOwner(scope)
	if e != nil || !replay {
		return requestretry.Resume, e
	}
	if uuid == "" {
		uuid = message.Record.String("uuid")
	}
	output := protocol.NewJSONObject(
		protocol.JSONField{Name: "type", Value: quote("user")},
		protocol.JSONField{Name: "message", Value: raw(protocol.NewJSONObject(protocol.JSONField{Name: "role", Value: quote("user")}, protocol.JSONField{Name: "content", Value: attachment.Get("prompt")}))},
		protocol.JSONField{Name: "session_id", Value: quote(h.options.Controller.SessionID())},
		protocol.JSONField{Name: "parent_tool_use_id", Value: raw(nil)},
		protocol.JSONField{Name: "uuid", Value: quote(uuid)},
		protocol.JSONField{Name: "timestamp", Value: message.Record.Get("timestamp")},
		protocol.JSONField{Name: "isReplay", Value: raw(true)},
	)
	return h.enqueue(scope, output)
}
