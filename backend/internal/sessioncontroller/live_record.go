package sessioncontroller

import (
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

// LiveRecord is either an ordinary original envelope or the actual provider
// envelope owned by the shared assembler. It never accepts rendered Source
// bytes, an SDK-normalized message or a serialization callback as a live view.
type LiveRecord struct {
	Record    *protocol.JSONObject
	Assistant *streamhistory.Envelope
}

// LiveRecordProfile declares only this finite record boundary. It does not
// resolve hooks, admit SDK inputs, derive results or calculate costs.
type LiveRecordProfile struct {
	QuerySource                                      string
	ActiveHooks, DynamicSDKQueue, ResultOrAccounting bool
}

// RecordLive snapshots outer envelope/stamp fields at record time, retaining
// only the actual inner assistant message view until drain. RecordLive, Feed,
// Finish, scheduled drain and live-envelope observations must run on one event
// owner. The Writer mutex alone cannot protect an independently mutated model
// envelope. Schedule must post the drain to that owner, never marshal on a
// timer goroutine.
func (writer *Writer) RecordLive(profile LiveRecordProfile, messages []LiveRecord, promptID json.RawMessage) (json.RawMessage, error) {
	if writer.options.Bootstrap != nil {
		return nil, cooperative.ErrRestricted
	}
	if profile.QuerySource != "sdk" || profile.ActiveHooks || profile.DynamicSDKQueue || profile.ResultOrAccounting {
		return nil, capability("live recorder SDK admission/hooks/result/accounting host")
	}
	cleaned, err := cleanRecordForEnvironment(messages, nil, writer.options.Environment)
	if err != nil {
		return nil, err
	}
	records := make([]*protocol.JSONObject, 0, len(cleaned))
	live := make(map[*protocol.JSONObject]*streamhistory.Envelope, len(cleaned))
	for _, value := range cleaned {
		if value.live != nil {
			// Source snapshots outer fields at insert time, while retaining the
			// actual (possibly shallow-transformed) API message until drain.
			record := value.record.Clone()
			record.Set("message", json.RawMessage("null"))
			records = append(records, &record)
			live[&record] = value.live
		} else {
			records = append(records, value.record)
		}
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.liveEntries == nil {
		return nil, errors.New("live recorder writer is not initialized")
	}
	return writer.record(records, promptID, live)
}

func (writer *Writer) marshalLiveEntry(entry *protocol.JSONObject) ([]byte, error) {
	if original := writer.liveFileHistoryEntries[entry]; original != nil {
		payload, err := original.MarshalJSON()
		if err != nil {
			return nil, err
		}
		projected := entry.Clone()
		projected.Set("snapshot", payload)
		return projected.MarshalJSON()
	}
	value := writer.liveEntries[entry]
	if value == nil {
		return entry.MarshalJSON()
	}
	// This is executed only by the owning event loop. No background goroutine
	// reads Message, blocks or Usage while the assembler modifies them.
	message, err := value.MessageJSON()
	if err != nil {
		return nil, err
	}
	snapshot := entry.Clone()
	snapshot.Set("message", message)
	return snapshot.MarshalJSON()
}
