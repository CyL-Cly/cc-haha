package clibootstrap

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstate"
)

func (m *MainPrint) nativeSessionStateOptions() sessionstate.Options {
	return sessionstate.Options{OnState: func(_ *cooperative.Scope, state string, details *protocol.JSONObject) error {
		if m.remoteIO != nil {
			m.remoteIO.ReportState(state, details)
		}
		return nil
	}, OnMetadata: func(_ *cooperative.Scope, value protocol.JSONObject) error {
		if m.remoteIO != nil {
			m.remoteIO.ReportMetadata(value)
		}
		return nil
	}, EmitEvents: func() bool {
		var value string
		_ = json.Unmarshal(m.environment.Get("CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS"), &value)
		return envTruthyNative(value)
	}, EnqueueSDK: func(scope *cooperative.Scope, event protocol.JSONObject) error {
		if !scope.BelongsTo(m.Owner) {
			return cooperative.ErrRestricted
		}
		if m.options.Interactive {
			return nil
		}
		if len(m.nativeSDKStateEvents) >= 1000 {
			m.nativeSDKStateEvents = append(m.nativeSDKStateEvents[:0], m.nativeSDKStateEvents[1:]...)
		}
		m.nativeSDKStateEvents = append(m.nativeSDKStateEvents, event.Clone())
		return nil
	}}
}

// State events use the original bounded SDK queue. UUID/session envelopes are
// allocated when the real headless consumer drains it, never on enqueue.
func (m *MainPrint) nativeDrainSessionEvents(scope *cooperative.Scope) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	queued := m.nativeSDKStateEvents
	m.nativeSDKStateEvents = nil
	for _, event := range queued {
		event = event.Clone()
		event.Set("uuid", quoteMain(uuidMain()))
		event.Set("session_id", quoteMain(m.Controller.SessionID()))
		data, err := protocol.EncodeNDJSON(event)
		if err != nil {
			return err
		}
		if err = m.enqueue(data); err != nil {
			return err
		}
	}
	return nil
}

func (m *MainPrint) nativeRunState(scope *cooperative.Scope, state string) error {
	if err := m.Worker.Change(scope, state, nil); err != nil {
		return err
	}
	return m.nativeDrainSessionEvents(scope)
}
