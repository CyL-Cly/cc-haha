package application

import (
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// The public print consumer retains the full SDK array only for verbose JSON.
// Late task/control/state events belong to SDK streaming, so they must neither
// enter this array nor replace its final result. Retain raw envelopes until
// original JSON.stringify formatting, preserving UTF-16 code units and object
// enumeration instead of decoding them through encoding/json's Go strings.
type printJSONCollector struct {
	verbose  bool
	messages []json.RawMessage
	last     json.RawMessage
}

func (output *printJSONCollector) observe(data json.RawMessage) {
	message := obj(data)
	switch message.String("type") {
	case "control_response", "control_request", "control_cancel_request", "stream_event", "keep_alive", "streamlined_text", "streamlined_tool_use_summary", "prompt_suggestion":
		return
	case "system":
		switch message.String("subtype") {
		case "session_state_changed", "task_notification", "task_started", "task_progress", "post_turn_summary":
			return
		}
	}
	copy := append(json.RawMessage(nil), data...)
	output.last = copy
	if output.verbose {
		output.messages = append(output.messages, copy)
	}
}

func (output *printJSONCollector) document() (json.RawMessage, error) {
	if len(output.last) == 0 || obj(output.last).String("type") != "result" {
		return nil, errors.New("No messages returned")
	}
	if !output.verbose {
		return protocol.ParseAndStringifyJavaScriptJSON(output.last)
	}
	encoded := json.RawMessage{'['}
	for index, message := range output.messages {
		if index > 0 {
			encoded = append(encoded, ',')
		}
		encoded = append(encoded, message...)
	}
	return protocol.ParseAndStringifyJavaScriptJSON(append(encoded, ']'))
}
