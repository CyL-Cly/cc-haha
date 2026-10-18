package clibootstrap

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (m *MainPrint) nativeTerminalSpinnerObserver() func(*cooperative.Scope, queryloop.Yield) error {
	if !m.options.Interactive || m.agentChild || m.options.OnTerminalSpinner == nil {
		return nil
	}
	return m.observeNativeTerminalSpinner
}

func (m *MainPrint) observeNativeTerminalSpinner(scope *cooperative.Scope, value queryloop.Yield) error {
	if !m.terminalFullscreenBound(scope) {
		return cooperative.ErrRestricted
	}
	progress := tui.TerminalSpinnerProgress{}
	if value.RequestStart {
		progress.Mode = "requesting"
		progress.QueryEvent = json.RawMessage(`{"type":"stream_request_start"}`)
	} else if value.Stream != nil {
		var changed bool
		var err error
		progress, changed, err = tui.TerminalSpinnerStreamProgress(value.Stream.Event)
		if err != nil || !changed {
			return err
		}

		envelope := tuiSpinnerQueryEnvelope(value.Stream.Event)
		progress.QueryEvent = envelope
	} else if value.Message != nil {
		raw, err := value.Message.MarshalJSON()
		if err != nil {
			return err
		}
		progress.QueryEvent = append(json.RawMessage(nil), raw...)
	} else {
		return nil
	}
	if m.options.OnTerminalSpinner != nil {
		return m.options.OnTerminalSpinner(progress)
	}
	return nil
}

func tuiSpinnerQueryEnvelope(event json.RawMessage) json.RawMessage {
	value := struct {
		Type  string          `json:"type"`
		Event json.RawMessage `json:"event"`
	}{Type: "stream_event", Event: append(json.RawMessage(nil), event...)}
	raw, _ := json.Marshal(value)
	return raw
}
