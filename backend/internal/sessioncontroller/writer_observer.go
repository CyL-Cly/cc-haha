package sessioncontroller

import "encoding/json"

// writerObservation is a diagnostic copy of already admitted writer state.
// It never carries an owner, Scope, live record, Set or readiness capability.
// The normal writer has no observer; that path does not take a snapshot, add a
// Promise reaction, or change any returned future.
type writerObservation struct {
	Phase    string            `json:"phase"`
	Record   []json.RawMessage `json:"record"`
	Selected []json.RawMessage `json:"selected"`
	UUID     json.RawMessage   `json:"uuid"`
	Set      []json.RawMessage `json:"set"`
	SetCount int               `json:"setCount"`
	Tracked  int               `json:"tracked"`
	Queued   int               `json:"queued"`
	Memo     json.RawMessage   `json:"memo"`
}

func observedRecordUUIDs(values []recordOriginal) []json.RawMessage {
	result := make([]json.RawMessage, len(values))
	for i, value := range values {
		result[i] = append(json.RawMessage(nil), value.record.Get("uuid")...)
	}
	return result
}

// observeWriter must be called on the existing owner and outside Writer.mu.
// This only reads the cache if it already exists. In particular observation
// cannot start/prime a UUID load, await anything, or enqueue an owner callback.
func (pending *PendingWriter) observeWriter(phase string, record, selected []recordOriginal, uuid json.RawMessage) {
	observer := pending.observeWriterState
	if observer == nil {
		return
	}
	value := writerObservation{
		Phase: phase, Record: observedRecordUUIDs(record), Selected: observedRecordUUIDs(selected),
		UUID: append(json.RawMessage(nil), uuid...), Set: make([]json.RawMessage, 0), Tracked: pending.tracked,
	}
	writer := pending.writer
	writer.mu.Lock()
	sid := writer.currentSessionID()
	value.Memo = append(json.RawMessage(nil), writer.lastPrompt...)
	for _, queue := range writer.queues {
		value.Queued += len(queue)
	}
	writer.mu.Unlock()
	if cache := pending.uuidCache; cache != nil {
		if future := cache.promises[sid]; future != nil && future.set != nil {
			value.SetCount = future.set.Len()
			for _, key := range future.set.Keys() {
				value.Set = append(value.Set, key.Raw())
			}
		}
	}
	observer(value)
}
