package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// REPL appendSystemMessage can run after the foreground ASK has returned. Only
// the current sealed dream continuation may append its original completion
// note; this does not admit arbitrary background model messages or another ASK.
func (a *SerialASK) AppendNativeMemorySaved(scope *cooperative.Scope, record protocol.JSONObject) (*RecordFuture, error) {
	if !scope.IsNativeAutoDream(a.owner) || record.String("type") != "system" || record.String("subtype") != "memory_saved" || a.pending == nil || !a.controller.options.AsyncPersistence {
		return nil, cooperative.ErrRestricted
	}
	copy := record.Clone()
	a.controller.messages = append(a.controller.messages, &copy)
	options, err := a.controller.recordOptions(scope)
	if err != nil {
		return nil, err
	}
	values := make([]LiveRecord, 0, len(a.controller.messages))
	for _, message := range a.controller.messages {
		if envelope := a.live[message]; envelope != nil {
			values = append(values, LiveRecord{Assistant: envelope})
		} else {
			values = append(values, LiveRecord{Record: message})
		}
	}
	source := a.controller.options.QuerySource
	if source == "" {
		source = "sdk"
	}
	return a.pending.BeginRecordLive(scope, a.controller.options.ResumeOptions.Loader, LiveRecordProfile{QuerySource: source}, values, options)
}
